/**
 * Copyright (C) 2021 The poly network Authors
 * This file is part of The poly network library.
 *
 * The poly network is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Lesser General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * The poly network is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Lesser General Public License for more details.
 *
 * You should have received a copy of the GNU Lesser General Public License
 * along with the poly network.  If not, see <http://www.gnu.org/licenses/>.
 *
 */

package main

import (
	"context"
	"flag"
	"os"
	"syscall"

	"github.com/polynetwork/flow-voter/config"
	"github.com/polynetwork/flow-voter/pkg/log"
	"github.com/polynetwork/flow-voter/pkg/voter"
	"github.com/zhiqiangxu/util/signal"
)

var confFile string
var zionHeight uint64
var flowHeight uint64

func init() {
	flag.StringVar(&confFile, "conf", "./config.json", "configuration file path")
	flag.Uint64Var(&zionHeight, "poly", 0, "specify poly start height")
	flag.Uint64Var(&flowHeight, "flow", 0, "specify flow start height")
	flag.Parse()
}

func main() {
	log.InitLog(log.InfoLog, "./Log/", log.Stdout)

	conf, err := config.LoadConfig(confFile)
	if err != nil {
		log.Fatalf("LoadConfig fail:%v", err)
	}
	if zionHeight > 0 {
		conf.ForceConfig.PolyHeight = zionHeight
	}
	if flowHeight > 0 {
		conf.ForceConfig.FlowHeight = flowHeight
	}

	v := voter.New(conf)

	ctx, cancelFunc := context.WithCancel(context.Background())
	signal.SetupHandler(func(sig os.Signal) {
		cancelFunc()
	}, syscall.SIGINT, syscall.SIGTERM)
	v.Start(ctx)

}
