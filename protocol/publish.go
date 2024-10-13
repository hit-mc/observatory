package protocol

type TargetStat struct {
	Target       Target               `json:"target"`
	Observations []SourcedObservation `json:"observations"`
}

type SourcedObservation struct {
	TargetObservation
	Source string `json:"source,omitempty"`
}
