package protocol

import "github.com/hit-mc/observatory/config"

type TargetStat struct {
	Target       config.Target       `json:"target"`
	Observations []SourceObservation `json:"observations"`
}

type SourceObservation struct {
	Observation
	Source string `json:"source,omitempty"`
}

type TargetSourceObservation struct {
	TargetObservation
	Source string `json:"source,omitempty"`
}
