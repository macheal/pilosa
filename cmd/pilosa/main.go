// Copyright 2017 Pilosa Corp.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

/*
This is the entrypoint for the Pilosa binary.
*/
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/pilosa/pilosa/cmd"
	_ "net/http/pprof"
)

func main() {
	//这里实现了远程获取pprof数据的接口
	go func() {
		log.Println(http.ListenAndServe("localhost:32222", nil))
	}()
	go func() {
		var now time.Time
		for {
			time.Sleep(3 * time.Minute)

			now = time.Now()
			debug.FreeOSMemory()
			log.Println("goFreeOSMemory", "call FreeOSMemory cost ", time.Now().Sub(now))
		}
	}()

	rootCmd := cmd.NewRootCommand(os.Stdin, os.Stdout, os.Stderr)
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
