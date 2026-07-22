package jobs

import (
	"context"

	"github.com/bograh/cargo/internal/metrics"
	"github.com/riverqueue/river"
)

type MetricsArgs struct{}

func (MetricsArgs) Kind() string { return "collect_metrics" }

type MetricsWorker struct {
	river.WorkerDefaults[MetricsArgs]
	Collector *metrics.Collector
}

func (w *MetricsWorker) Work(ctx context.Context, _ *river.Job[MetricsArgs]) error {
	return w.Collector.CollectOnce(ctx)
}
