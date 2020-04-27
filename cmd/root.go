// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of go-perun. Use
// of this source code is governed by a MIT-style license that can be found in
// the LICENSE file.

package cmd

import (
	"fmt"
	"os"
	"runtime/debug"

	"perun.network/go-perun/log"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:              "perun",
	Short:            "Perun Network umbrella executable",
	Long:             "Umbrella project for demonstrators and tests of the Perun Project.",
	PersistentPreRun: runRoot,
}

var cfgFile, cfgNetFile string

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "config.yaml", "General config file")
	rootCmd.PersistentFlags().StringVar(&cfgNetFile, "network", "network.yaml", "Network config file")
	rootCmd.PersistentFlags().StringVar(&logConfig.Level, "log-level", "warn", "Logrus level")
	viper.BindPFlag("log.level", rootCmd.PersistentFlags().Lookup("log-level"))
	rootCmd.PersistentFlags().StringVar(&logConfig.File, "log-file", "", "log file")
	viper.BindPFlag("log.file", rootCmd.PersistentFlags().Lookup("log-file"))
}

// initConfig reads the config and sets the loglevel.
// The demo configuration will be parsed in the
func initConfig() {
	// Load config files
	viper.SetConfigFile(cfgFile)
	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file, %s", err)
	}

	viper.SetConfigFile(cfgNetFile)
	if err := viper.MergeInConfig(); err != nil {
		log.Fatalf("Error reading network config file, %s", err)
	}
}

func runRoot(c *cobra.Command, args []string) {
	SetConfig()
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
