// Copyright (c) 2019 Chair of Applied Cryptography, Technische Universität
// Darmstadt, Germany. All rights reserved. This file is part of
// perun-eth-demo. Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package demo

import (
	"fmt"
	"os"
	"strings"

	"github.com/pkg/errors"
	"perun.network/go-perun/log"
)

type argument struct {
	Name      string
	Validator func(string) error
}

type command struct {
	Name     string
	Args     []argument
	Help     string
	Function func([]string) error
}

var commands []command
var cmdInput chan string

func init() {
	cmdInput = make(chan string, 1)

	commands = []command{
		{
			"connect",
			[]argument{{"Peer", valAlias}},
			"Connect to a peer by their alias. The connection allows payment channels to be opened with the given peer.\nExample: connect bob",
			func(args []string) error { return backend.Connect(args) },
		}, {
			"open",
			[]argument{{"Peer", valAlias}, {"Our Balance", valBal}, {"Their Balance", valBal}},
			"Open a payment channel with the given peer and balances. The first value is the own balance and the second value is the peers balance. It is only possible to open one channel per peer.\nExample: open alice 10 10",
			func(args []string) error { return backend.Open(args) },
		}, {
			"send",
			[]argument{{"Peer", valPeer}, {"Amount", valBal}},
			"Send a payment with amount to a given peer over the established channel.\nExample: send alice 5",
			func(args []string) error { return backend.Send(args) },
		}, {
			"close",
			[]argument{{"Peer", valPeer}},
			"Close a the channel with the given peer. This will push the latest state to the block chain.\nExample: close alice",
			func(args []string) error { return backend.Close(args) },
		}, {
			"config",
			nil,
			"Print the current configuration and known peers.",
			func([]string) error { return backend.PrintConfig() },
		}, {
			"info",
			nil,
			"Print information about funds, peers, and channels.",
			func(args []string) error { return backend.Info(args) },
		}, {
			"benchmark",
			[]argument{{"Peer", valPeer}, {"amount", valUInt}, {"txCount", valUInt}},
			"Performs a benchmark with the given peer by sending amount ETH in txCount micro transactions. Must have an open channel with the peer.",
			func(args []string) error { return backend.Benchmark(args) },
		}, {
			"help",
			nil,
			"Prints all possible commands.",
			printHelp,
		}, {
			"exit",
			nil,
			"Exits the program.",
			func(args []string) error {
				if err := backend.Exit(args); err != nil {
					log.Error("err while exiting: ", err)
				}
				os.Exit(0)
				return nil
			},
		},
	}
}

var prompts = make(chan func(string), 1)

// AddInput adds an input to the input command queue.
func AddInput(in string) {
	select {
	case f := <-prompts:
		f(in)
	default:
		if err := Execute(in); err != nil {
			fmt.Println("\033[0;33m⚡\033[0m", err)
		}
	}
}

// Prompt waits for input on the command line and then executes the given
// function with the input.
func Prompt(msg string, f func(string)) {
	PrintfAsync(msg)
	prompts <- f
}

// PrintfAsync prints the given message for an asynchronous event. More
// precisely, the message is prepended with a newline and appended with the
// command prefix.
func PrintfAsync(format string, a ...interface{}) {
	fmt.Printf("\r"+format+"> ", a...)
}

// Execute interprets commands entered by the user.
func Execute(in string) error {
	in = strings.TrimSpace(in)
	args := strings.Split(in, " ")
	command := args[0]
	args = args[1:]

	log.Tracef("Reading command '%s'\n", command)
	for _, cmd := range commands {
		if cmd.Name == command {
			if len(args) != len(cmd.Args) {
				return errors.Errorf("Invalid number of arguments, expected %d but got %d", len(cmd.Args), len(args))
			}
			for i, arg := range args {
				if err := cmd.Args[i].Validator(arg); err != nil {
					return errors.WithMessagef(err, "'%s' argument invalid for '%s': %v", cmd.Args[i].Name, command, arg)
				}
			}
			return cmd.Function(args)
		}
	}
	if len(command) > 0 {
		return errors.Errorf("Unknown command: %s. Enter \"help\" for a list of commands.", command)
	}
	return nil
}

func printHelp(args []string) error {
	for _, cmd := range commands {
		fmt.Print(cmd.Name, " ")
		for _, arg := range cmd.Args {
			fmt.Printf("<%s> ", arg.Name)
		}
		fmt.Printf("\n\t%s\n\n", strings.ReplaceAll(cmd.Help, "\n", "\n\t"))
	}

	return nil
}
