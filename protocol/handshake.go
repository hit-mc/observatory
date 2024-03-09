package protocol

const (
	TokenHeader      = "X-Observatory-Token"
	ObserverIDHeader = "X-Observatory-Observer-ID"
)

// ServerPushInfo is the first server->client packet, applying configs to observers
type ServerPushInfo struct {
	Version uint64   `json:"server_push_info_version"`
	Targets []Target `json:"targets"`
}

const CurrentServerPushInfoVersion uint64 = 240308
