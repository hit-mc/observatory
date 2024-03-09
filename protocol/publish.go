package protocol

type TargetStats map[string][]SourcedObservation

type SourcedObservation struct {
	Observation
	Source string `json:"source,omitempty"`
}
