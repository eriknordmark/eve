// Copyright (c) 2018-2020 Zededa, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lf-edge/eve/pkg/pillar/agentlog"
	"github.com/lf-edge/eve/pkg/pillar/base"
	"github.com/lf-edge/eve/pkg/pillar/cmd/baseosmgr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/client"
	"github.com/lf-edge/eve/pkg/pillar/cmd/collectinfo"
	"github.com/lf-edge/eve/pkg/pillar/cmd/command"
	"github.com/lf-edge/eve/pkg/pillar/cmd/conntrack"
	"github.com/lf-edge/eve/pkg/pillar/cmd/diag"
	"github.com/lf-edge/eve/pkg/pillar/cmd/domainmgr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/downloader"
	"github.com/lf-edge/eve/pkg/pillar/cmd/executor"
	"github.com/lf-edge/eve/pkg/pillar/cmd/faultinjection"
	"github.com/lf-edge/eve/pkg/pillar/cmd/hardwaremodel"
	"github.com/lf-edge/eve/pkg/pillar/cmd/ipcmonitor"
	"github.com/lf-edge/eve/pkg/pillar/cmd/ledmanager"
	"github.com/lf-edge/eve/pkg/pillar/cmd/loguploader"
	"github.com/lf-edge/eve/pkg/pillar/cmd/monitor"
	"github.com/lf-edge/eve/pkg/pillar/cmd/nim"
	"github.com/lf-edge/eve/pkg/pillar/cmd/nodeagent"
	"github.com/lf-edge/eve/pkg/pillar/cmd/pbuf"
	"github.com/lf-edge/eve/pkg/pillar/cmd/tpmmgr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/upgradeconverter"
	"github.com/lf-edge/eve/pkg/pillar/cmd/usbmanager"
	"github.com/lf-edge/eve/pkg/pillar/cmd/vaultmgr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/vcomlink"
	"github.com/lf-edge/eve/pkg/pillar/cmd/verifier"
	"github.com/lf-edge/eve/pkg/pillar/cmd/volumemgr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/waitforaddr"
	"github.com/lf-edge/eve/pkg/pillar/cmd/watcher"
	"github.com/lf-edge/eve/pkg/pillar/cmd/wstunnelclient"
	"github.com/lf-edge/eve/pkg/pillar/cmd/zedagent"
	"github.com/lf-edge/eve/pkg/pillar/cmd/zedkube"
	"github.com/lf-edge/eve/pkg/pillar/cmd/zedmanager"
	"github.com/lf-edge/eve/pkg/pillar/cmd/zedrouter"
	"github.com/lf-edge/eve/pkg/pillar/cmd/zfsmanager"
	"github.com/lf-edge/eve/pkg/pillar/controllerconn"
	"github.com/lf-edge/eve/pkg/pillar/pubsub"
	"github.com/lf-edge/eve/pkg/pillar/pubsub/socketdriver"
	_ "github.com/lf-edge/eve/pkg/pillar/rstats"
	"github.com/lf-edge/eve/pkg/pillar/types"
	"github.com/sirupsen/logrus"
)

const (
	agentName = "zedbox"
)

// The function returns an exit value
type entrypoint struct {
	f types.AgentRunner
}

var (
	entrypoints = map[string]entrypoint{
		"client":           {f: client.Run},
		"command":          {f: command.Run},
		"diag":             {f: diag.Run},
		"domainmgr":        {f: domainmgr.Run},
		"downloader":       {f: downloader.Run},
		"executor":         {f: executor.Run},
		"faultinjection":   {f: faultinjection.Run},
		"hardwaremodel":    {f: hardwaremodel.Run},
		"ledmanager":       {f: ledmanager.Run},
		"loguploader":      {f: loguploader.Run},
		"nim":              {f: nim.Run},
		"nodeagent":        {f: nodeagent.Run},
		"verifier":         {f: verifier.Run},
		"volumemgr":        {f: volumemgr.Run},
		"waitforaddr":      {f: waitforaddr.Run},
		"zedagent":         {f: zedagent.Run},
		"zedkube":          {f: zedkube.Run},
		"zedmanager":       {f: zedmanager.Run},
		"zedrouter":        {f: zedrouter.Run},
		"ipcmonitor":       {f: ipcmonitor.Run},
		"baseosmgr":        {f: baseosmgr.Run},
		"wstunnelclient":   {f: wstunnelclient.Run},
		"conntrack":        {f: conntrack.Run},
		"pbuf":             {f: pbuf.Run},
		"tpmmgr":           {f: tpmmgr.Run},
		"vaultmgr":         {f: vaultmgr.Run},
		"upgradeconverter": {f: upgradeconverter.Run},
		"watcher":          {f: watcher.Run},
		"zfsmanager":       {f: zfsmanager.Run},
		"usbmanager":       {f: usbmanager.Run},
		"collectinfo":      {f: collectinfo.Run},
		"vcomlink":         {f: vcomlink.Run},
		"monitor":          {f: monitor.Run},
	}
	logger *logrus.Logger
	log    *base.LogObject
)

func main() {
	// Check what service we are intending to start.
	basename := filepath.Base(os.Args[0])
	logger, log = agentlog.Init(basename)
	if sep, ok := entrypoints[basename]; ok {
		retval := runService(basename, sep)
		os.Exit(retval)
	}
	// XXX we should do InitializeCertDir some different way
	// If this zedbox?
	if basename == agentName {
		err := controllerconn.InitializeCertDir(log)
		if err != nil {
			log.Fatal(err)
		}
	}
	fmt.Printf("zedbox: Unknown package: %s\n", basename)
	os.Exit(1)
}

func runService(serviceName string, sep entrypoint) int {
	arguments := os.Args[1:]
	log.Functionf("Running inline command %s args: %+v",
		serviceName, arguments)
	ps := pubsub.New(
		&socketdriver.SocketDriver{Logger: logger, Log: log},
		logger, log)
	return sep.f(ps, logger, log, arguments, "")
}
