package commands

import (
	"errors"
	"os"

	"github.com/concourse/concourse/atc"
	"github.com/concourse/concourse/fly/commands/internal/displayhelpers"
	"github.com/concourse/concourse/fly/rc"
	"github.com/concourse/concourse/fly/ui"
	"github.com/fatih/color"
)

type PipelinesCommand struct {
	AllTeams bool     `short:"a"  long:"all-teams" description:"Show all pipelines for all available teams"`
	Teams    []string `short:"n" long:"team" description:"Show pipelines for the given teams"`
	Json     bool     `long:"json" description:"Print command result as JSON"`
}

func (command *PipelinesCommand) Execute([]string) error {
	target, err := rc.LoadTarget(Fly.Target, Fly.Verbose)
	if err != nil {
		return err
	}

	err = target.Validate()
	if err != nil {
		return err
	}

	if len(command.Teams) > 0 && command.AllTeams {
		return errors.New("Cannot specify both --all-teams and --team")
	}

	var headers []string
	var pipelines []atc.Pipeline

	if command.AllTeams {
		pipelines, err = target.Client().ListPipelines()
		headers = []string{"name", "team", "paused", "public"}
	} else if len(command.Teams) > 0 {
		client := target.Client()
		for _, teamName := range command.Teams {
			atcTeam := client.Team(teamName)
			teamPipelines, err := atcTeam.ListPipelines()
			if err != nil {
				return err
			}
			pipelines = append(pipelines, teamPipelines...)
		}
		headers = []string{"name", "team", "paused", "public"}
	} else {
		pipelines, err = target.Team().ListPipelines()
		headers = []string{"name", "paused", "public"}
	}
	if err != nil {
		return err
	}

	if command.Json {
		err = displayhelpers.JsonPrint(pipelines)
		if err != nil {
			return err
		}
		return nil
	}

	table := ui.Table{Headers: ui.TableRow{}}
	for _, h := range headers {
		table.Headers = append(table.Headers, ui.TableCell{Contents: h, Color: color.New(color.Bold)})
	}

	for _, p := range pipelines {
		var pausedColumn ui.TableCell
		if p.Paused {
			pausedColumn.Contents = "yes"
			pausedColumn.Color = ui.OnColor
		} else {
			pausedColumn.Contents = "no"
		}

		var publicColumn ui.TableCell
		if p.Public {
			publicColumn.Contents = "yes"
			publicColumn.Color = ui.OnColor
		} else {
			publicColumn.Contents = "no"
		}

		row := ui.TableRow{}
		row = append(row, ui.TableCell{Contents: p.Name})
		if command.AllTeams || len(command.Teams) > 0 {
			row = append(row, ui.TableCell{Contents: p.TeamName})
		}
		row = append(row, pausedColumn)
		row = append(row, publicColumn)

		table.Data = append(table.Data, row)
	}

	return table.Render(os.Stdout, Fly.PrintTableHeaders)
}
