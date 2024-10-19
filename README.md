# Batchy

Your friendly micro batching library written in go.
I think you understand what micro batching is if you're reading this ;)
Implemented with a frequency and a size.
The process will send when either the frequency expires after the first received job or when the size is reached.

# Usage
## General Process
1. implement the `BatchProcessor` interface, this will take an array of type `Job` and return an array of `JobResult`.
2. implement the `Job` and `JobResult` interfaces
   - any struct implementing the `Job` interface is required to implement `func Id() string`
   - the `JobResult` is required to implement the function `func JobId() string` 
   - this is so `Job` and `JobResults` can be matched together for returning. 
3. after implementing an appropriate processor, pass this into `batchy.NewMicroBatch` to create a new batchy micro batch
   - with configuration as required for your process you must configure a size > 0 and non nil frequency 
4. call MicroBatch.Start() to start consuming and processing jobs
5. call the ProcessJob function with a single `Job` and the `JobResult` will be returned when the job has been processed

## Important Notes
If the id/jobId do not match or if they are non-unique, then they will not get the `JobResult` matching up to the `Job` and then no result will be returned.
Or could be returned to the wrong `Job` if the id is non-unique

It is important to monitor the `ctx.Done()` channel in the `BatchProcessor` as if the MicroBatch is shutdown it will cancel the context
associated with any running BatchProcessors.

## Example
See this example where we implement the `BatchProcessor` as a `Doubler` which will double the passed in `ExampleJobs` 'I' value. Returning an
`ExampleJobResult` which contains I and a `Doubled` result value.
See the code for this example in the [doubler_example.go](examples/doubler_example.go)

Note how the `Doubler.Process` func fills out the `ExampleJob` and `ExampleJobResult` id and jobId appropriately.

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
	id string
	I  int
}

func (exampleJob ExampleJob) Id() string {
	return exampleJob.id
}

type ExampleJobResult struct {
	jobId   string
	I       int
	Doubled int
}

func (exampleJobResult ExampleJobResult) JobId() string {
	return exampleJobResult.jobId
}

type Doubler struct{} // example batch processor

// note the use of Done() to handle when the process should shut down
func (Doubler) Process(ctx context.Context, jobs []ExampleJob) (jobResult []ExampleJobResult, ok bool) {
	doubleChannel := make(chan []ExampleJobResult)
	go func() {
		result := make([]ExampleJobResult, len(jobs))
		for i, job := range jobs {
			result[i] = ExampleJobResult{
				jobId:   job.Id(),
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

	// call Start so the MicroBatch will listen
	wg.Add(1)
	go func() {
		// if you wanted to send some context/state to the processor
		// this could be done via the context
		microBatch.Start(context.Background())
		wg.Done()
	}()

	// you could source your job from anywhere, this is just a contrived example
	// submit a single job
	for i := 0; i < 10; i++ {
		result := microBatch.ProcessJob(ExampleJob{
			id: fmt.Sprintf("%d-%d", time.Nanosecond, i),
			I:  i,
		}).Doubled
		fmt.Printf("doubled %d to %d\n", i, result)
	}

	// finished sending jobs, time to shut down
	microBatch.ShutDown()
	wg.Wait()
}
```

# Development
Use the `./scripts/check.sh` script to run go check and lint targets

# Out of scope
- just used print functions to log for convenience given this is an example library
- no extensive error handling for this example code
- didn't publish this to anywhere so `go get` will not work
- consider a worker pool or go routine limit for the `BatchProcessor` go routines. Not sure if this necessary but depending on the BatchProcessor, memory and cpu usage could be a problem eventually.
