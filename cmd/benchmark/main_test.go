package benchmark_test

import (
	"context"
	"testing"

	"github.com/perun-network/perun-eth-demo/cmd/benchmark"
	"github.com/sirupsen/logrus"
	"perun.network/go-perun/log"
	plogrus "perun.network/go-perun/log/logrus"
)

func init() {
	// Configure logging.
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	log.Set(plogrus.FromLogrus(logger))
}

func TestExecute(t *testing.T) {
	ctx := context.Background()
	network := "ganache" // One of [ganache, optimism, arbitrum].
	mnemonic := "pistol kiwi shrug future ozone ostrich match remove crucial oblige cream critic"
	benchmark.Execute(ctx, network, mnemonic)
}
