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
