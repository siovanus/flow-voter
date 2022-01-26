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

package config

import (
	"encoding/json"
	"io/ioutil"
	"sync"
)

type Config struct {
	sync.Once
	PolyConfig       PolyConfig
	FlowConfig       FlowConfig
	ForceConfig      ForceConfig
	BoltDbPath       string
	WhitelistMethods []string
	whitelistMethods map[string]bool
}

func (c *Config) IsWhitelistMethod(method string) bool {
	c.Do(func() {
		c.whitelistMethods = map[string]bool{}
		for _, m := range c.WhitelistMethods {
			c.whitelistMethods[m] = true
		}
	})

	return c.whitelistMethods[method]
}

type PolyConfig struct {
	RestURL                 string
	EntranceContractAddress string
	WalletFile              string
	WalletPwd               string
}

type FlowConfig struct {
	SideChainId uint64
	EventType   string
	GrpcURL     []string
}

type ForceConfig struct {
	PolyHeight uint32
	FlowHeight uint64
}

func LoadConfig(confFile string) (config *Config, err error) {
	jsonBytes, err := ioutil.ReadFile(confFile)
	if err != nil {
		return
	}

	config = &Config{}
	err = json.Unmarshal(jsonBytes, config)
	return
}
