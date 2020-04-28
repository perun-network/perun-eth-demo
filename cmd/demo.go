// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package cmd

import (
	"fmt"

	demo "github.com/perun-network/perun-eth-demo/cmd/demo"

	prompt "github.com/c-bata/go-prompt"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Two party payment Demo",
	Long: `Enables two user to send payments between each other in a ledger state channel.
	The channels are funded and settled on an Ethereum blockchain, leaving out the dispute case.

	It illustrates what Perun is capable of.`,
	Run: runDemo,
}

var testAPI bool

func init() {
	rootCmd.AddCommand(demoCmd)
	demoCmd.PersistentFlags().BoolVar(&testAPI, "test-api", false, "Expose testing API at 8080")
	demoCmd.PersistentFlags().StringVar(&demo.GetConfig().SecretKey, "sk", "", "ETH Secret Key")
	viper.BindPFlag("secretkey", demoCmd.PersistentFlags().Lookup("sk"))
}

// runDemo is executed everytime the program is started with the `demo` sub-command.
func runDemo(c *cobra.Command, args []string) {
	demo.Setup()
	if testAPI {
		demo.StartTestAPI()
	}
	p := prompt.New(
		executor,
		completer,
		prompt.OptionPrefix("> "),
		prompt.OptionTitle("perun"),
	)
	p.Run()
}

func completer(prompt.Document) []prompt.Suggest {
	return []prompt.Suggest{}
}

// executor wraps the demo executor to print error messages.
func executor(in string) {
	if err := demo.Executor(in); err != nil {
		fmt.Println("\033[0;33m⚡\033[0m", err)
	}
}
