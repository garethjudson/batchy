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
