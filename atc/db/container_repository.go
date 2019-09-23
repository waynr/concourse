package db

import (
	"fmt"
	"github.com/concourse/concourse/atc/db/lock"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/concourse/concourse/atc"
)

//go:generate counterfeiter . ContainerRepository

type ContainerRepository interface {
	FindOrphanedContainers() ([]CreatingContainer, []CreatedContainer, []DestroyingContainer, error)
	DestroyFailedContainers() (int, error)
	FindDestroyingContainers(workerName string) ([]string, error)
	RemoveDestroyingContainers(workerName string, currentHandles []string) (int, error)
	UpdateContainersMissingSince(workerName string, handles []string) error
	RemoveMissingContainers(time.Duration) (int, error)

	VisibleContainers([]string) ([]Container, error)
	AllContainers() ([]Container, error)
}

type containerRepository struct {
	conn Conn
	lockFactory lock.LockFactory // TODO
}

func NewContainerRepository(conn Conn, lockFactory lock.LockFactory) ContainerRepository {
	return &containerRepository{
		conn: conn,
		lockFactory: lockFactory,
	}
}

func diff(a, b []string) (diff []string) {
	m := make(map[string]bool)

	for _, item := range b {
		m[item] = true
	}

	for _, item := range a {
		if _, ok := m[item]; !ok {
			diff = append(diff, item)
		}
	}

	return
}

func (repository *containerRepository) queryContainerHandles(cond sq.Eq) ([]string, error) {
	query, args, err := psql.Select("handle").From("containers").Where(cond).ToSql()
	if err != nil {
		return nil, err
	}

	rows, err := repository.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}

	defer Close(rows)

	handles := []string{}

	for rows.Next() {
		var handle = "handle"
		columns := []interface{}{&handle}

		err = rows.Scan(columns...)
		if err != nil {
			return nil, err
		}
		handles = append(handles, handle)
	}

	return handles, nil
}

func (repository *containerRepository) UpdateContainersMissingSince(workerName string, reportedHandles []string) error {
	// clear out missing_since for reported containers
	query, args, err := psql.Update("containers").
		Set("missing_since", nil).
		Where(
			sq.And{
				sq.NotEq{
					"missing_since": nil,
				},
				sq.Eq{
					"handle": reportedHandles,
				},
			},
		).ToSql()
	if err != nil {
		return err
	}

	rows, err := repository.conn.Query(query, args...)
	if err != nil {
		return err
	}

	Close(rows)

	dbHandles, err := repository.queryContainerHandles(sq.Eq{
		"worker_name":   workerName,
		"missing_since": nil,
	})
	if err != nil {
		return err
	}

	handles := diff(dbHandles, reportedHandles)

	query, args, err = psql.Update("containers").
		Set("missing_since", sq.Expr("now()")).
		Where(sq.And{
			sq.Eq{"handle": handles},
			sq.NotEq{"state": atc.ContainerStateCreating},
		}).ToSql()
	if err != nil {
		return err
	}

	rows, err = repository.conn.Query(query, args...)
	if err != nil {
		return err
	}

	defer Close(rows)

	return nil
}

func (repository *containerRepository) FindDestroyingContainers(workerName string) ([]string, error) {
	return repository.queryContainerHandles(
		sq.Eq{
			"state":        atc.ContainerStateDestroying,
			"worker_name":  workerName,
			"discontinued": false,
		},
	)
}

func (repository *containerRepository) RemoveMissingContainers(gracePeriod time.Duration) (int, error) {
	result, err := psql.Delete("containers").
		Where(
			sq.And{
				sq.Eq{
					"state": []string{atc.ContainerStateCreated, atc.ContainerStateFailed},
				},
				sq.Gt{
					"NOW() - missing_since": fmt.Sprintf("%.0f seconds", gracePeriod.Seconds()),
				},
			},
		).RunWith(repository.conn).
		Exec()

	if err != nil {
		return 0, err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(affected), nil
}

func (repository *containerRepository) RemoveDestroyingContainers(workerName string, handlesToIgnore []string) (int, error) {
	rows, err := psql.Delete("containers").
		Where(
			sq.And{
				sq.Eq{
					"worker_name": workerName,
				},
				sq.NotEq{
					"handle": handlesToIgnore,
				},
				sq.Eq{
					"state": atc.ContainerStateDestroying,
				},
			},
		).RunWith(repository.conn).
		Exec()

	if err != nil {
		return 0, err
	}

	affected, err := rows.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(affected), nil
}

func (repository *containerRepository) FindOrphanedContainers() ([]CreatingContainer, []CreatedContainer, []DestroyingContainer, error) {
	query, args, err := selectContainers("c").
		LeftJoin("builds b ON b.id = c.build_id").
		LeftJoin("containers icc ON icc.id = c.image_check_container_id").
		LeftJoin("containers igc ON igc.id = c.image_get_container_id").
		Where(sq.Or{
			sq.Eq{
				"c.build_id":                         nil,
				"c.image_check_container_id":         nil,
				"c.image_get_container_id":           nil,
				"c.resource_config_check_session_id": nil,
			},
			sq.And{
				sq.NotEq{"c.build_id": nil},
				sq.Eq{"b.interceptible": false},
			},
			sq.And{
				sq.NotEq{"c.image_check_container_id": nil},
				sq.NotEq{"icc.state": atc.ContainerStateCreating},
			},
			sq.And{
				sq.NotEq{"c.image_get_container_id": nil},
				sq.NotEq{"igc.state": atc.ContainerStateCreating},
			},
		}).
		ToSql()
	if err != nil {
		return nil, nil, nil, err
	}

	rows, err := repository.conn.Query(query, args...)
	if err != nil {
		return nil, nil, nil, err
	}

	defer Close(rows)

	creatingContainers := []CreatingContainer{}
	createdContainers := []CreatedContainer{}
	destroyingContainers := []DestroyingContainer{}

	var (
		creatingContainer   CreatingContainer
		createdContainer    CreatedContainer
		destroyingContainer DestroyingContainer
	)

	for rows.Next() {
		creatingContainer, createdContainer, destroyingContainer, _, err = scanContainer(rows, repository.conn)
		if err != nil {
			return nil, nil, nil, err
		}

		if creatingContainer != nil {
			creatingContainers = append(creatingContainers, creatingContainer)
		}

		if createdContainer != nil {
			createdContainers = append(createdContainers, createdContainer)
		}

		if destroyingContainer != nil {
			destroyingContainers = append(destroyingContainers, destroyingContainer)
		}
	}

	err = rows.Err()
	if err != nil {
		return nil, nil, nil, err
	}

	return creatingContainers, createdContainers, destroyingContainers, nil
}

/////////////////////////////////////////////////////////////////////////////
//func getContainers(teamSelect sq.SelectBuilder, repository *containerRepository) ([]Container, error) {
//
//	rows, err := selectContainers("c").
//		Join("workers w ON c.worker_name = w.name").
//		Join("resource_config_check_sessions rccs ON rccs.id = c.resource_config_check_session_id").
//		Join("resources r ON r.resource_config_id = rccs.resource_config_id").
//		Join("pipelines p ON p.id = r.pipeline_id").
//		Where(teamSelect).
//		Where(sq.Or{
//			teamSelect,
//			sq.Eq{
//				"w.team_id": nil,
//			},
//		}).
//		Distinct().
//		RunWith(repository.conn).
//		Query()
//	if err != nil {
//		return nil, err
//	}
//
//	var containers []Container
//	containers, err = scanContainers(rows, repository.conn, containers)
//	if err != nil {
//		return nil, err
//	}
//
//	rows, err = selectContainers("c").
//		Join("workers w ON c.worker_name = w.name").
//		Join("resource_config_check_sessions rccs ON rccs.id = c.resource_config_check_session_id").
//		Join("resource_types rt ON rt.resource_config_id = rccs.resource_config_id").
//		Join("pipelines p ON p.id = rt.pipeline_id").
//		Where(sq.Eq{
//			"p.team_id": t.id,
//		}).
//		Where(sq.Or{
//			teamSelect,
//			sq.Eq{
//				"w.team_id": nil,
//			},
//		}).
//		Distinct().
//		RunWith(repository.conn).
//		Query()
//	if err != nil {
//		return nil, err
//	}
//
//	containers, err = scanContainers(rows, repository.conn, containers)
//	if err != nil {
//		return nil, err
//	}
//
//	rows, err = selectContainers("c").
//		Where(teamSelect).
//		RunWith(t.conn).
//		Query()
//	if err != nil {
//		return nil, err
//	}
//
//	containers, err = scanContainers(rows, t.conn, containers)
//	if err != nil {
//		return nil, err
//	}
//
//	return containers, nil
//}

/////////////////////////////////////////////////////////////////////////////
func selectContainersWithColumns(additionalColumns []string, asOptional ...string) sq.SelectBuilder {
	columns := []string{"id", "handle", "worker_name", "hijacked", "discontinued", "state"}

	columns = append(columns, containerMetadataColumns...)

	table := "containers"
	if len(asOptional) > 0 {
		as := asOptional[0]
		for i, c := range columns {
			columns[i] = as + "." + c
		}

		table += " " + as
	}
	columns = append(columns, additionalColumns...)
	return psql.Select(columns...).From(table)
}

func selectContainers (asOptional ...string) sq.SelectBuilder{
	return selectContainersWithColumns([]string{}, asOptional...)
}

func scanContainer(row sq.RowScanner, conn Conn) (CreatingContainer, CreatedContainer, DestroyingContainer, FailedContainer, error) {
	var (
		id             int
		handle         string
		workerName     string
		isDiscontinued bool
		isHijacked     bool
		state          string

		metadata ContainerMetadata
	)

	columns := []interface{}{&id, &handle, &workerName, &isHijacked, &isDiscontinued, &state}
	columns = append(columns, metadata.ScanTargets()...)

	err := row.Scan(columns...)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	switch state {
	case atc.ContainerStateCreating:
		return newCreatingContainer(
			id,
			handle,
			workerName,
			metadata,
			conn,
		), nil, nil, nil, nil
	case atc.ContainerStateCreated:
		return nil, newCreatedContainer(
			id,
			handle,
			workerName,
			metadata,
			isHijacked,
			conn,
		), nil, nil, nil
	case atc.ContainerStateDestroying:
		return nil, nil, newDestroyingContainer(
			id,
			handle,
			workerName,
			metadata,
			isDiscontinued,
			conn,
		), nil, nil
	case atc.ContainerStateFailed:
		return nil, nil, nil, newFailedContainer(
			id,
			handle,
			workerName,
			metadata,
			conn,
		), nil
	}

	return nil, nil, nil, nil, nil
}

func (repository *containerRepository) DestroyFailedContainers() (int, error) {
	result, err := sq.Delete("containers").
		Where(sq.Eq{"containers.state": atc.ContainerStateFailed}).
		PlaceholderFormat(sq.Dollar).
		RunWith(repository.conn).
		Exec()
	if err != nil {
		return 0, err
	}

	failedContainersLen, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}

	return int(failedContainersLen), nil
}

func (repository *containerRepository) AllContainers() ([]Container, error) {
	rows, err := selectContainersWithColumns( []string{"t.name"}, "c").
		Join("workers w ON c.worker_name = w.name").
		Join("resource_config_check_sessions rccs ON rccs.id = c.resource_config_check_session_id").
		Join("resources r ON r.resource_config_id = rccs.resource_config_id").
		Join("pipelines p ON p.id = r.pipeline_id").
		Join("teams t ON t.id = p.team_id").
		Distinct().
		RunWith(repository.conn).
		Query()

	if err != nil {
		return nil, err
	}
	return scanContainers(rows, repository.conn, []Container{})
}

func (repository *containerRepository) VisibleContainers(teamNames []string) ([]Container, error) {
	rows, err := selectContainers("c").
		Join("resource_config_check_sessions rccs ON rccs.id = c.resource_config_check_session_id").
		Join("resources r ON r.resource_config_id = rccs.resource_config_id").
		Join("pipelines p ON p.id = r.pipeline_id").
		Join("teams t ON t.id = p.team_id").
		Where("in ('monitoring-hush-house')").
		Distinct().
		RunWith(repository.conn).
		Query()

	if err != nil {
		return nil, err
	}

	var containers []Container
	currentTeamContainers, err := scanContainers(rows, repository.conn, containers)
	if err != nil {
		return nil, err
	}

	//TODO: figure out whether we do have otherTeamPublicContainers

	//rows, err = pipelinesQuery.
	//	Where(sq.NotEq{"t.name": teamNames}).
	//	Where(sq.Eq{"public": true}).
	//	OrderBy("team_id ASC", "ordering ASC").
	//	RunWith(repository.conn).
	//	Query()
	//if err != nil {
	//	return nil, err
	//}
	//
	//otherTeamPublicContainers, err := scanContainers(rows, repository.conn, containers)
	//if err != nil {
	//	return nil, err
	//}
	//return append(currentTeamContainers, otherTeamPublicContainers...), nil

	return currentTeamContainers, nil
}
