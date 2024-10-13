package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/akamensky/argparse"
	"github.com/bombsimon/logrusr/v4"
	"github.com/go-logr/logr"
	"github.com/hit-mc/observatory/config"
	"github.com/hit-mc/observatory/observatory"
	"github.com/sirupsen/logrus"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func main() {
	parser := argparse.NewParser("observatory", "Minecraft tunnel observatory")
	configFile := parser.String("c", "config", &argparse.Options{
		Required: true,
		Help:     "Config file to use",
		Default:  "config.toml",
	})
	isServer := parser.Flag("", "server", &argparse.Options{
		Required: false,
		Help:     "Run program as server (stats collector)",
		Default:  false,
	})
	isClient := parser.Flag("", "client", &argparse.Options{
		Required: false,
		Help:     "Run program as client (stats observer&reporter)",
		Default:  false,
	})
	err := parser.Parse(os.Args)
	if err != nil {
		fmt.Print(parser.Usage(err))
		return
	}
	if !*isServer && !*isClient {
		fmt.Println("You must specify either --server or --client in CLI arguments.")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())

	logger := logrusr.New(logrus.New())
	go func() {
		logger := logger.WithName("SignalHandler")
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		logger.Info("stopping")
		cancel()
	}()

	if *isServer {
		runCollector(ctx, *configFile, logger)
	} else {
		runObserver(ctx, *configFile, logger)
	}
}

func runCollector(ctx context.Context, configFile string, logger logr.Logger) {
	cfg, err := config.Read[config.Server](configFile)
	if err != nil {
		panic(fmt.Errorf("error reading config file `%v`: %w", configFile, err))
	}

	handshakeTimeout := time.Duration(cfg.HandshakeTimeout) * time.Millisecond
	observationLiveTime := time.Duration(cfg.ObservationLiveTime) * time.Millisecond
	if observationLiveTime < time.Second {
		observationLiveTime = 300 * time.Second
	}
	s := observatory.NewCollector(cfg.Listen, handshakeTimeout, logger, cfg.Token, cfg.Targets, observationLiveTime)
	err = s.Run(ctx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}

func runObserver(ctx context.Context, configFile string, logger logr.Logger) {
	cfg, err := config.Read[config.Client](configFile)
	if err != nil {
		panic(fmt.Errorf("error reading config file `%v`: %w", configFile, err))
	}

	sendBuf := cfg.SendBuffer
	if sendBuf < 0 {
		sendBuf = 0
	}

	reconnInterval := cfg.ReconnectInterval
	if reconnInterval < 0 {
		reconnInterval = 0
	}

	reportConnTimeout := cfg.ReportConnectTimeout
	if reportConnTimeout < 0 {
		reportConnTimeout = 0
	}

	observatory.RunClient(ctx, cfg, logger)
}
