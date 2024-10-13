package observatory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/gorilla/websocket"
	"github.com/hit-mc/observatory/config"
	"github.com/hit-mc/observatory/protocol"
	"github.com/samber/lo"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"
)

func NewCollector(
	listen string,
	handshakeTimeout time.Duration,
	logger logr.Logger,
	token string,
	targets []config.Target,
) *Collector {
	targets2 := make([]protocol.Target, len(targets))
	for i := range targets {
		targets2[i] = protocol.Target{
			Host: targets[i].Host,
			Port: uint16(targets[i].Port),
		}
	}
	return &Collector{
		listen:           listen,
		handshakeTimeout: handshakeTimeout,
		logger:           logger,
		stats:            newServerStatus(targets),
		token:            token,
		targets:          targets2,
	}
}

type Collector struct {
	listen           string
	handshakeTimeout time.Duration
	logger           logr.Logger
	stats            serverStatus
	token            string
	targets          []protocol.Target
}

func (c *Collector) Run(ctx context.Context) error {
	ssc, err := json.Marshal(protocol.ServerPushInfo{
		Version: protocol.CurrentServerPushInfoVersion,
		Targets: c.targets,
	})
	if err != nil {
		return fmt.Errorf("error marshalling ServerPushInfo: %w", err)
	}

	up := websocket.Upgrader{
		HandshakeTimeout: c.handshakeTimeout,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		stats := c.stats.GetStats()
		data, err := json.Marshal(stats)
		if err != nil {
			w.WriteHeader(500)
			_, _ = io.WriteString(w, fmt.Sprintf("error marshalling JSON: %v", err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/collect", func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			c.logger.Error(err, "error upgrading connection protocol to websocket")
			return
		}
		defer func() {
			_ = conn.Close()
		}()
		token := r.Header.Get(protocol.TokenHeader)
		if token != c.token {
			c.logger.Error(nil, "reject connection with invalid token", "token", token)
			return
		}
		observerID := r.Header.Get(protocol.ObserverIDHeader)
		if observerID == "" {
			return
		}
		c.logger.Info("observer connected successfully",
			"endpoint", r.RemoteAddr, "observer_id", observerID)

		const pingInterval = 10 * time.Second
		const maxPingWait = 20 * time.Second
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(pingInterval + maxPingWait))
			return nil
		})

		writeMu := &sync.Mutex{}
		writeMessage := func(typ int, data []byte) error {
			writeMu.Lock()
			defer writeMu.Unlock()
			return conn.WriteMessage(websocket.BinaryMessage, ssc)
		}

		wg := &sync.WaitGroup{}
		// send keepalive packets periodically
		wg.Add(1)
		go func() {
			for {
				err := writeMessage(websocket.PingMessage, nil)
				if err != nil {
					c.logger.Error(err, "error pinging client, disconnect",
						"observer_id", observerID)
					_ = conn.Close()
					return
				}
				sleep(r.Context(), pingInterval)
			}
		}()
		defer wg.Wait()

		// send server-side config
		err = writeMessage(websocket.BinaryMessage, ssc)
		if err != nil {
			c.logger.Error(err, "error writing server-side-config")
			return
		}

		for {
			typ, data, err := conn.ReadMessage()
			if err != nil {
				c.logger.Error(err, "error reading websocket message")
				return
			}
			if typ != websocket.BinaryMessage {
				continue
			}
			var observation protocol.TargetObservation
			err = json.Unmarshal(data, &observation)
			if err != nil {
				c.logger.Error(err, "error unmarshalling message from client")
				return
			}
			c.stats.Put(&observation, observerID)
		}
	})
	server := http.Server{
		Addr:              c.listen,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       300 * time.Second,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	err = server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("error starting HTTP server: %w", err)
	}
	return nil
}

func newServerStatus(targets []config.Target) serverStatus {
	return serverStatus{
		status:  make(map[protocol.Target][]protocol.TargetSourceObservation),
		targets: targets,
	}
}

type serverStatus struct {
	mu      sync.RWMutex
	status  map[protocol.Target][]protocol.TargetSourceObservation
	targets []config.Target
}

func (s *serverStatus) Put(observation *protocol.TargetObservation, source string) {
	entry := protocol.TargetSourceObservation{
		TargetObservation: *observation,
		Source:            source,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// replace existing entry with the same observer
	for i, ob := range s.status[observation.Target] {
		if ob.Source == source {
			s.status[observation.Target][i] = entry
			goto done
		}
	}
	// add new entry if not exist
	s.status[observation.Target] = append(s.status[observation.Target], entry)
	slices.SortFunc(s.status[observation.Target], func(a, b protocol.TargetSourceObservation) int {
		return cmp.Compare(a.Source, b.Source)
	})
done:
	s.purge()
}

// purge removes state entries lived longer than 180s
// Note: caller should hold write lock
func (s *serverStatus) purge() {
	const ttl = 180 * time.Second
	t0 := time.Now().Add(-ttl)
	for target := range s.status {
		ss := s.status[target]
		purged := false
		for i := range ss {
			if time.Time(ss[i].Time).Before(t0) {
				ss[i].Time = protocol.TimeStamp{}
				purged = true
			}
		}
		if purged {
			s.status[target] = shrink(ss)
		}
	}
}

func shrink(s []protocol.TargetSourceObservation) []protocol.TargetSourceObservation {
	i := 0 // slow pointer
	j := 0 // fast pointer
	for j < len(s) {
		if !time.Time(s[j].Time).IsZero() {
			i++
		}
		j++
		if j < len(s) {
			s[i] = s[j]
		}
	}
	if i != j {
		return s[:i]
	}
	return s
}

func (s *serverStatus) GetStats() []protocol.TargetStat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return lo.Map(s.targets, func(item config.Target, _ int) protocol.TargetStat {
		target := protocol.Target{
			Host: item.Host,
			Port: uint16(item.Port),
		}
		return protocol.TargetStat{
			Target: item,
			Observations: lo.Map(s.status[target],
				func(item protocol.TargetSourceObservation, _ int) protocol.SourceObservation {
					return protocol.SourceObservation{
						Observation: item.Observation,
						Source:      item.Source,
					}
				}),
		}
	})
}
