// Copyright (c) 2017-2022 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"

	"github.com/lf-edge/eve/pkg/pillar/agentlog"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/pubsub/socketdriver"
)

func main() {
	logger, log := agentlog.Init(agentName)
	arguments := os.Args[1:]
	ps := pubsub.New(
		&socketdriver.SocketDriver{Logger: logger, Log: log},
		logger, log)
	retval := Run(ps, logger, log, arguments, "")
	os.Exit(retval)
}
