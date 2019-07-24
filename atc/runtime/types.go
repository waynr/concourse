package runtime

import (
	"io"

	"github.com/concourse/concourse/atc"
)

const (
	InitializingEvent = "Initializing"
	StartingEvent     = "Starting"
	FinishedEvent     = "Finished"
)

type Event struct {
	EventType     string
	ExitStatus    int
	VersionResult VersionResult
}

type IOConfig struct {
	Stdout io.Writer
	Stderr io.Writer
}

type VersionResult struct {
	Version  atc.Version         `json:"version"`
	Metadata []atc.MetadataField `json:"metadata,omitempty"`
}

type PutRequest struct {
	Source atc.Source `json:"source"`
	Params atc.Params `json:"params,omitempty"`
}
