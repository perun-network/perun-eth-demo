// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"bufio"
	"fmt"
	"os"

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

// CommandLineFlags contains the command line flags.
type CommandLineFlags struct {
	testAPIEnabled bool
	cfgFile        string
	cfgNetFile     string
	useStdIO       bool
}

var flags CommandLineFlags

func init() {
	demoCmd.PersistentFlags().StringVar(&flags.cfgFile, "config", "config.yaml", "General config file")
	demoCmd.PersistentFlags().StringVar(&flags.cfgNetFile, "network", "network.yaml", "Network config file")
	demoCmd.PersistentFlags().BoolVar(&flags.testAPIEnabled, "test-api", false, "Expose testing API at 8080")
	demoCmd.PersistentFlags().BoolVar(&GetConfig().Node.PersistenceEnabled, "persistence", false, "Enables the persistence")
	demoCmd.PersistentFlags().StringVar(&GetConfig().SecretKey, "sk", "", "ETH Secret Key")
	viper.BindPFlag("secretkey", demoCmd.PersistentFlags().Lookup("sk"))
	demoCmd.PersistentFlags().BoolVar(&flags.useStdIO, "stdio", false, "Read from stdin")
}

// GetDemoCmd exposes demoCmd so that it can be used as a sub-command by another cobra command instance.
func GetDemoCmd() *cobra.Command {
	return demoCmd
}

// runDemo is executed everytime the program is started with the `demo` sub-command.
func runDemo(c *cobra.Command, args []string) {
	Setup()
	if flags.testAPIEnabled {
		StartTestAPI()
	}
	if flags.useStdIO {
		runWithStdIO(executor)
	} else {
		p := prompt.New(
			executor,
			completer,
			prompt.OptionPrefix("> "),
			prompt.OptionTitle("perun"),
		)
		p.Run()
	}
}

func runWithStdIO(executor func(string)) {
	stdinChannel := newStdinChannel()
	for {
		fmt.Printf("> ")
		inputLine, ok := <-stdinChannel
		if !ok {
			fmt.Println("Input channel closed")
			os.Exit(0)
		}
		executor(inputLine)
	}
}

func newStdinChannel() chan string {
	inputChannel := make(chan string)
	go func() {
		defer close(inputChannel)
		inputScanner := bufio.NewScanner(os.Stdin)
		for inputScanner.Scan() {
			inputChannel <- inputScanner.Text()
		}
	}()
	return inputChannel
}

func completer(prompt.Document) []prompt.Suggest {
	return []prompt.Suggest{}
}

// executor wraps the demo executor to print error messages.
func executor(in string) {
	AddInput(in)
}
