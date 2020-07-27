// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package cmd

import (
	"fmt"
	"os"
	"runtime/debug"

	"perun.network/go-perun/log"

	"github.com/perun-network/perun-eth-demo/cmd/demo"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:              "perun-eth-demo",
	Short:            "Perun State Channels Demo",
	Long:             "Demonstrator for the Perun state channel framework using the Ethereum backend.",
	PersistentPreRun: runRoot,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&logConfig.Level, "log-level", "warn", "Logrus level")
	viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	rootCmd.PersistentFlags().StringVar(&logConfig.File, "log-file", "", "log file")
	viper.BindPFlag("log.file", rootCmd.PersistentFlags().Lookup("log-file"))

	rootCmd.AddCommand(demo.GetDemoCmd())
}

func runRoot(c *cobra.Command, args []string) {
	setConfig()
}

// Execute called by rootCmd
func Execute() {
	defer func() {
		if err := recover(); err != nil {
			log.Panicf("err=%s, trace=%s\n", err, debug.Stack())
		}
	}()

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(0)
	}
}
