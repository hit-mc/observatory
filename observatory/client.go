package observatory

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-logr/logr"
	"github.com/gorilla/websocket"
	"github.com/hit-mc/observatory/config"
	"github.com/hit-mc/observatory/protocol"
	"github.com/xrjr/mcutils/pkg/ping"
	"net/http"
	"slices"
	"sync"
	"time"
)

func RunClient(ctx context.Context, cfg *config.Client, logger logr.Logger) {

	// [Communicator] -> [intermediate goroutine] -> [Pinger]
	configUpdates := make(chan *protocol.ServerPushInfo, 8)
	// [Pinger] -> [Communicator]
	pingResults := make(chan *protocol.TargetObservation, cfg.SendBuffer)

	pinger := Pinger{
		Logger:   logger.WithName("pinger"),
		Output:   pingResults,
		Interval: time.Duration(cfg.CheckInterval) * time.Millisecond,
	}
	comm := Communicator{
		ServerAddr:        cfg.ReportServer,
		ConnectTimeout:    time.Duration(cfg.ReportConnectTimeout) * time.Millisecond,
		ReconnectInterval: time.Duration(cfg.ReconnectInterval) * time.Millisecond,
		Token:             cfg.Token,
		ObserverID:        cfg.ObserverID,
		Logger:            logger.WithName("communicator"),
		ConfigUpdate:      configUpdates,
		StateReport:       pingResults,
	}

	wg := &sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(configUpdates)
		comm.Run(ctx)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for cu := range configUpdates {
			// apply config updates to pinger
			pinger.UpdateTargets(cu.Targets)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(pingResults)
		pinger.Run(ctx)
	}()

	wg.Wait()
}

type Observer struct {
	serverAddr           string
	interval             time.Duration
	observations         chan *protocol.TargetObservation
	logger               logr.Logger
	reconnectInterval    time.Duration
	reportConnectTimeout time.Duration
	token                string
	id                   string
}

func readServerPushInfo(c *websocket.Conn) (*protocol.ServerPushInfo, error) {
	for {
		typ, data, err := c.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read websocket message: %w", err)
		}
		if typ != websocket.BinaryMessage {
			continue
		}
		var info protocol.ServerPushInfo
		err = json.Unmarshal(data, &info)
		if err != nil {
			return nil, fmt.Errorf("unmarshal ServerPushInfo: %w", err)
		}
		if info.Version != protocol.CurrentServerPushInfoVersion {
			return nil, fmt.Errorf("invalid ServerPushInfo version `%v`, raw data: `%v`",
				info.Version, string(data))
		}
		return &info, nil
	}
}

type Pinger struct {
	targets  []protocol.Target
	mu       sync.RWMutex
	Logger   logr.Logger
	Output   chan<- *protocol.TargetObservation
	Interval time.Duration
}

func (p *Pinger) Run(ctx context.Context) {
	tk := time.NewTicker(p.Interval)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			p.ping()
		}
	}
}

func (p *Pinger) ping() {
	var targets []protocol.Target
	func() {
		p.mu.RLock()
		defer p.mu.RUnlock()
		targets = slices.Clone(p.targets)
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for i := range targets {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			p.Output <- ping0(targets[i], p.Logger)
		}()
	}
}

func ping0(target protocol.Target, log logr.Logger) *protocol.TargetObservation {
	t := time.Now()
	_, latency, err := ping.Ping(target.Host, int(target.Port))
	if err != nil {
		log.Error(err, "ping failed", "target", target.String())
		return &protocol.TargetObservation{
			Target: target,
			Observation: protocol.Observation{
				Time:   protocol.TimeStamp(t),
				Online: false,
			},
		}
	}
	return &protocol.TargetObservation{
		Target: target,
		Observation: protocol.Observation{
			Time:    protocol.TimeStamp(t),
			Online:  true,
			Latency: uint32(latency),
		},
	}
}

func (p *Pinger) UpdateTargets(targets []protocol.Target) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = slices.Clone(targets)
}

type Communicator struct {
	ServerAddr        string
	ConnectTimeout    time.Duration
	ReconnectInterval time.Duration
	Token             string
	ObserverID        string
	Logger            logr.Logger
	ConfigUpdate      chan<- *protocol.ServerPushInfo
	StateReport       <-chan *protocol.TargetObservation
	sendBuffer        []*protocol.TargetObservation // states that are taken out but not sent
}

// Run blocks until StateReport or ctx is closed
func (c *Communicator) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.run(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.Logger.Error(err, "waiting for reconnect")
			sleep(ctx, c.ReconnectInterval)
			continue
		}
		return
	}
}

func (c *Communicator) run(ctx context.Context) error {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: c.ConnectTimeout,
	}
	headers := http.Header{
		protocol.TokenHeader:      []string{c.Token},
		protocol.ObserverIDHeader: []string{c.ObserverID},
	}
	conn, _, err := dialer.Dial(c.ServerAddr, headers)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	serverConfig, err := readServerPushInfo(conn)
	if err != nil {
		return fmt.Errorf("error reading server side config: %w", err)
	}
	c.Logger.Info("received observe targets from server", "targets", serverConfig.Targets)

	c.ConfigUpdate <- serverConfig

	sendData := func(ob *protocol.TargetObservation) error {
		data, err := json.Marshal(*ob)
		if err != nil {
			c.Logger.Error(err, "error marshalling observation result, drop")
			return nil
		}
		return conn.WriteMessage(websocket.BinaryMessage, data)
	}

	for len(c.sendBuffer) > 0 {
		err := sendData(c.sendBuffer[0])
		if err != nil {
			c.Logger.Error(err, "error writing websocket message, try reconnect")
			return err
		}
		c.sendBuffer = c.sendBuffer[1:]
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ob, ok := <-c.StateReport:
			if !ok {
				return nil
			}
			err := sendData(ob)
			if err != nil {
				c.Logger.Error(err, "error writing websocket message, try reconnect")
				c.sendBuffer = append(c.sendBuffer, ob) // store unsent messages to queue
				return err
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
	case <-tm.C:
	}
}
