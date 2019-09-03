package accessor

import (
	"fmt"
	"strings"

	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/atc/db"
	jwt "github.com/dgrijalva/jwt-go"
)

//go:generate counterfeiter . Access

type Access interface {
	HasToken() bool
	IsAuthenticated() bool
	IsAuthorized(string) bool
	IsAdmin() bool
	IsSystem() bool
	TeamNames() []string
	CSRFToken() string
	UserName() string
}

type access struct {
	*jwt.Token
	action      string
	teamFactory db.TeamFactory
}

func (a *access) HasToken() bool {
	return a.Token != nil
}

func (a *access) IsAuthenticated() bool {
	return a.HasToken() && a.Token.Valid
}

func (a *access) Claims() jwt.MapClaims {
	if a.IsAuthenticated() {
		if claims, ok := a.Token.Claims.(jwt.MapClaims); ok {
			return claims
		}
	}
	return jwt.MapClaims{}
}

func (a *access) IsAuthorized(teamName string) bool {

	if a.IsAdmin() {
		return true
	}

	team, found, err := a.teamFactory.FindTeam(teamName)
	if err != nil {
		return false
	}

	if !found {
		return false
	}

	for _, teamRole := range a.TeamRoles(team.Auth()) {
		if a.hasPermission(teamRole) {
			return true
		}
	}

	return false
}

func (a *access) TeamNames() []string {

	teamNames := []string{}

	teams, err := a.teamFactory.GetTeams()
	if err != nil {
		return teamNames
	}

	for _, team := range teams {
		for _, teamRole := range a.TeamRoles(team.Auth()) {
			if a.hasPermission(teamRole) {
				teamNames = append(teamNames, team.Name())
			}
		}
	}

	return teamNames
}

func (a *access) TeamRoles(auth atc.TeamAuth) []string {

	roles := []string{}

	groups := a.Claims()["groups"]
	connectorID := a.FederatedClaims()["connector_id"]
	userID := a.FederatedClaims()["user_id"]
	userName := a.FederatedClaims()["username"]

	for role, auth := range auth {
		userAuth := auth["users"]
		groupAuth := auth["groups"]

		// backwards compatibility for allow-all-users
		if len(userAuth) == 0 && len(groupAuth) == 0 {
			roles = append(roles, role)
		}

		for _, user := range userAuth {
			if userID != "" {
				if strings.EqualFold(user, fmt.Sprintf("%v:%v", connectorID, userID)) {
					roles = append(roles, role)
				}
			}
			if userName != "" {
				if strings.EqualFold(user, fmt.Sprintf("%v:%v", connectorID, userName)) {
					roles = append(roles, role)
				}
			}
		}

		if claimGroups, ok := groups.([]string); ok {
			for _, group := range groupAuth {
				for _, claimGroup := range claimGroups {
					if claimGroup != "" {

						// match the provider plus the entire claim group e.g. github:org-name:team-name
						if strings.EqualFold(group, fmt.Sprintf("%v:%v", connectorID, claimGroup)) {
							roles = append(roles, role)
						}

						if parts := strings.Split(claimGroup, ":"); len(parts) > 0 {
							// match the provider plus the org e.g. github:org-name
							if strings.EqualFold(group, fmt.Sprintf("%v:%v", connectorID, parts[0])) {
								roles = append(roles, role)
							}
						}
					}
				}
			}
		}
	}

	return roles
}

func (a *access) hasPermission(role string) bool {
	switch requiredRoles[a.action] {
	case "owner":
		return role == "owner"
	case "member":
		return role == "owner" || role == "member"
	case "pipeline-operator":
		return role == "owner" || role == "member" || role == "pipeline-operator"
	case "viewer":
		return role == "owner" || role == "member" || role == "pipeline-operator" || role == "viewer"
	default:
		return false
	}
}

func (a *access) FederatedClaims() map[string]interface{} {
	if claims, ok := a.Claims()["federated_claims"]; ok {
		parsed, ok := claims.(map[string]interface{})
		if ok {
			return parsed
		}
	}

	return map[string]interface{}{}
}

func (a *access) ConnectorID() string {
	var connectorID string
	if connectorClaim, ok := a.FederatedClaims()["connector_id"]; ok {
		connectorID, _ = connectorClaim.(string)
	}
	return connectorID
}

func (a *access) UserID() string {
	var userID string
	if userClaim, ok := a.FederatedClaims()["user_id"]; ok {
		userID, _ = userClaim.(string)
	}
	return userID
}

func (a *access) IsAdmin() bool {
	return a.ConnectorID() == "client" && a.UserID() == "client-id"
}

func (a *access) IsSystem() bool {
	return a.ConnectorID() == "client" && a.UserID() == "client-id"
}

func (a *access) CSRFToken() string {
	if csrfTokenClaim, ok := a.Claims()["csrf"]; ok {
		if csrfToken, ok := csrfTokenClaim.(string); ok {
			return csrfToken
		}
	}
	return ""
}

func (a *access) UserName() string {
	if userName, ok := a.Claims()["user_name"]; ok {
		if userName, ok := userName.(string); ok {
			return userName
		}
	} else if systemName, ok := a.Claims()["system"]; ok {
		if systemName == true {
			return "system"
		}
	}
	return ""
}

var requiredRoles = map[string]string{
	atc.SaveConfig:                    "member",
	atc.GetConfig:                     "viewer",
	atc.GetCC:                         "viewer",
	atc.GetBuild:                      "viewer",
	atc.GetBuildPlan:                  "viewer",
	atc.CreateBuild:                   "member",
	atc.ListBuilds:                    "viewer",
	atc.BuildEvents:                   "viewer",
	atc.BuildResources:                "viewer",
	atc.AbortBuild:                    "pipeline-operator",
	atc.GetBuildPreparation:           "viewer",
	atc.GetJob:                        "viewer",
	atc.CreateJobBuild:                "pipeline-operator",
	atc.ListAllJobs:                   "viewer",
	atc.ListJobs:                      "viewer",
	atc.ListJobBuilds:                 "viewer",
	atc.ListJobInputs:                 "viewer",
	atc.GetJobBuild:                   "viewer",
	atc.PauseJob:                      "pipeline-operator",
	atc.UnpauseJob:                    "pipeline-operator",
	atc.GetVersionsDB:                 "viewer",
	atc.JobBadge:                      "viewer",
	atc.MainJobBadge:                  "viewer",
	atc.ClearTaskCache:                "pipeline-operator",
	atc.ListAllResources:              "viewer",
	atc.ListResources:                 "viewer",
	atc.ListResourceTypes:             "viewer",
	atc.GetResource:                   "viewer",
	atc.UnpinResource:                 "pipeline-operator",
	atc.SetPinCommentOnResource:       "pipeline-operator",
	atc.CheckResource:                 "pipeline-operator",
	atc.CheckResourceWebHook:          "pipeline-operator",
	atc.CheckResourceType:             "pipeline-operator",
	atc.ListResourceVersions:          "viewer",
	atc.GetResourceVersion:            "viewer",
	atc.EnableResourceVersion:         "pipeline-operator",
	atc.DisableResourceVersion:        "pipeline-operator",
	atc.PinResourceVersion:            "pipeline-operator",
	atc.ListBuildsWithVersionAsInput:  "viewer",
	atc.ListBuildsWithVersionAsOutput: "viewer",
	atc.GetResourceCausality:          "viewer",
	atc.ListAllPipelines:              "viewer",
	atc.ListPipelines:                 "viewer",
	atc.GetPipeline:                   "viewer",
	atc.DeletePipeline:                "member",
	atc.OrderPipelines:                "member",
	atc.PausePipeline:                 "pipeline-operator",
	atc.UnpausePipeline:               "pipeline-operator",
	atc.ExposePipeline:                "member",
	atc.HidePipeline:                  "member",
	atc.RenamePipeline:                "member",
	atc.ListPipelineBuilds:            "viewer",
	atc.CreatePipelineBuild:           "member",
	atc.PipelineBadge:                 "viewer",
	atc.RegisterWorker:                "member",
	atc.LandWorker:                    "member",
	atc.RetireWorker:                  "member",
	atc.PruneWorker:                   "member",
	atc.HeartbeatWorker:               "member",
	atc.ListWorkers:                   "viewer",
	atc.DeleteWorker:                  "member",
	atc.SetLogLevel:                   "member",
	atc.GetLogLevel:                   "viewer",
	atc.DownloadCLI:                   "viewer",
	atc.GetInfo:                       "viewer",
	atc.GetInfoCreds:                  "viewer",
	atc.ListContainers:                "viewer",
	atc.GetContainer:                  "viewer",
	atc.HijackContainer:               "member",
	atc.ListDestroyingContainers:      "viewer",
	atc.ReportWorkerContainers:        "member",
	atc.ListVolumes:                   "viewer",
	atc.ListDestroyingVolumes:         "viewer",
	atc.ReportWorkerVolumes:           "member",
	atc.ListTeams:                     "viewer",
	atc.GetTeam:                       "viewer",
	atc.SetTeam:                       "owner",
	atc.RenameTeam:                    "owner",
	atc.DestroyTeam:                   "owner",
	atc.ListTeamBuilds:                "viewer",
	atc.CreateArtifact:                "member",
	atc.GetArtifact:                   "member",
	atc.ListBuildArtifacts:            "viewer",
}
