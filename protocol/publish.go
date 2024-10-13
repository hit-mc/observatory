package protocol

type TargetStat struct {
	Target       Target               `json:"target"`
	Observations []SourcedObservation `json:"observations"`
}

type SourcedObservation struct {
	Observation
	Source string `json:"source,omitempty"`
}
