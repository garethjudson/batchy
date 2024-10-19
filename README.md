# Batchy

Your friendly micro batching library written in go.
I think you understand what micro batching is if you're reading this ;)
Implemented with a frequency and a size.
The process will send when either the frequency expires after the first received job or when the size is reached.

# Usage

First implement the `BatchProcessor` interface, this will take an array of type `Job` (in effect interface{}),
It will return a processed array of `JobResult` (also interface{}) so in effect any type can be used.
`Job` and `JobResult` are generic, but named this way to provide meaning to the generics that would otherwise be used.

See this example where we implement a Doubler which will double the passed in ExampleJobs 'I' value. Returning an
ExampleJobResult which contains I and a Doubled result value.
See the code for this example in the [doubler_example.go](examples/doubler_example.go)
It is important to monitor the `ctx.Done()` channel as if the MicroBatch is shutdown it will cancel the context
associated with any running BatchProcessors.

After implementing an appropriate processor, pass this into a new MicroBatch, with configuration as required for your
use case.

Full Doubler example:
```go
package examples

import (
	"context"
	"fmt"
	"github.com/garethjudson/batchy"
	"sync"
	"time"
)

type ExampleJob struct {
	I int
}

type ExampleJobResult struct {
	I       int
	Doubled int
}

type Doubler struct{} // example batch processor

// note the use of Done() to handle when the process should shut down
func (Doubler) Process(ctx context.Context, jobs []ExampleJob) (jobResult []ExampleJobResult, ok bool) {
	doubleChannel := make(chan []ExampleJobResult)
	go func() {
		result := make([]ExampleJobResult, len(jobs))
		for i, job := range jobs {
			result[i] = ExampleJobResult{
				I:       job.I,
				Doubled: job.I * 2,
			}
		}
		doubleChannel <- result
	}()
	for {
		select {
		case <-ctx.Done():
			{
				close(doubleChannel)
				// didn't complete in time
				return nil, false
			}
		case result := <-doubleChannel:
			{
				close(doubleChannel)
				return result, true
			}
		}
	}
}

func RunDoubler() {
	inChannel := make(chan ExampleJob)
	outChannel := make(chan []ExampleJobResult)

	frequency := 30 * time.Millisecond
	microBatch, err := batchy.NewMicroBatch(batchy.MicroBatchConfig[ExampleJob, ExampleJobResult]{
		Size:           4,
		Frequency:      &frequency,
		BatchProcessor: Doubler{},
	})
	if err != nil {
		// you should probably handle the error but this is a somewhat contrived example
		panic(err)
	}
	var wg sync.WaitGroup

	// handle batch results
	go func() {
		for jobResultBatch := range outChannel {
			// process the result array here
			for _, jobResult := range jobResultBatch {
				fmt.Printf("doubled %d to %d", jobResult.I, jobResult.Doubled)
			}
		}
	}()

	// call Start so the MicroBatch will listen
	wg.Add(1)
	go func() {
		// if you wanted to send some context/state to the processor
		// this could be done via the context
		microBatch.Start(context.Background(), inChannel, outChannel)
		wg.Done()
	}()

	// send jobs into the inChannel
	go func() {
		// you could source your job from anywhere, this is just a contrived example
		// submit a single job
		job := ExampleJob{
			I: 5,
		}

		inChannel <- job

		// finished sending jobs, time to shut down
		microBatch.ShutDown()
	}()

	wg.Wait()
}
```

Additionally provide the in and out channel to send in Jobs and receive the JobResults. Results are returned in batches.
If you want to track which Job is related to which result, make sure you implement it in the BatchProcessor.
Any additional configuration or state can be provided to the context.

# Development
Use the `./scripts/check.sh` script to run go check and lint targets

# Out of scope
- just used println for convenience given this is an example library rather than a production grade logging library
- no extensive error handling for this example code
- didn't publish this to anywhere so `go get` will not work
- consider a worker pool for the BatchProcessor go routines. Not sure if this necessary but depending on the BatchProcessor, memory and cpu usage could be a problem eventually.
