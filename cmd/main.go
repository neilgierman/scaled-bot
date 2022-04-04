/*
Copyright 2018 The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"runtime"
	"time"

	"github.com/go-redis/redis"
	"github.com/google/uuid"
	"k8s.io/klog/v2"

	"github.com/neilgierman/scaled-bot/pkg/election"
	"github.com/neilgierman/scaled-bot/pkg/jobqueue"
)

const (
	streamName = "bot_jobs"
)

// this is where we define the main polling logic. This is what the elected master
// runs. Typically this would be getting into the DB and finding all of the resource
// IDs and add those IDs to a queue for a worker to later pick up. In this example
// we are just generating 20 jobs with randome IDs
func getMainPollingLogic(rdb *redis.Client) func(context.Context) {
	return func(ctx context.Context) {
		// complete your controller loop here
		klog.Info("Controller loop...")

		klog.Info("Creating 20 jobs...")
		for i := 1; i <= 20; i++ {
			if err := rdb.RPush(streamName, uuid.New().String()).Err(); err != nil {
				klog.Errorf("could not add data to queue: %w", err)
			}

			klog.Infof("%d added to queue", i)
		}
	}
}

// this is the worker logic. This is what executes each time a worker picks up
// the next message on the queue. In this example we are just pegging the CPU
// for 2 minutes so that we can test k8s hpa and make sure as more pods come
// online, they are not elected master and also they do not process the same
// message that other workers have already picked up
func getWorkerLogic() func(interface{}) {
	return func(job interface{}) {
		id := job.(string)
		klog.Infof("Processing ID %s", id)

		// peg CPUs for 2min to test scaling
		done := make(chan int)

		for i := 0; i < runtime.NumCPU(); i++ {
			go func() {
				for {
					select {
					case <-done:
						return
					default:
					}
				}
			}()
		}

		time.Sleep(time.Minute * 2)
		close(done)
	}
}

func main() {
	klog.InitFlags(nil)

	//create the Redis client and make sure it is up
	rdb := jobqueue.CreateRedisClient("redis-master:6379")
	
	//start processing any received job in redis
	//even the master is also a worker
	go jobqueue.StartProcessingQueue(rdb, streamName, getWorkerLogic())

	// the lock name is specific to this bot
	leaseLockName := "bot"
	// the lock namespace is this cluster's namespace
	leaseLockNamespace := "default"

	// run the election logic. This maintains the main polling logic. If this
	// pod is not the master, then it will not run the main polling logic
	// but be available to take over in case the current master dies or does
	// not renew its lease
	election.RunElection(getMainPollingLogic(rdb), leaseLockName, leaseLockNamespace)

}

