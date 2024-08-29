package mempool

import (
	"github.com/go-kit/kit/metrics"
)

const (
	// MetricsSubsystem is a subsystem shared by all metrics exposed by this
	// package.
	MetricsSubsystem = "mempool"
)

//go:generate go run ../scripts/metricsgen -struct=Metrics

// Metrics contains metrics exposed by this package.
// see MetricsProvider for descriptions.
type Metrics struct {
	// Number of uncommitted transactions in the mempool.
	Size metrics.Gauge

	// Total size of the mempool in bytes.
	SizeBytes metrics.Gauge

	// Number of uncommitted transactions in each lane.
	LaneSize metrics.Gauge `metrics_labels:"lane"`

	// Size in bytes of each lane.
	LaneBytes metrics.Gauge `metrics_labels:"lane"`

	// Duration in ms of a transaction in mempool.
	TxDuration metrics.Histogram `metrics_bucketsizes:"50,100,200,500,1000" metrics_labels:"lane"`

	// Histogram of transaction sizes in bytes.
	TxSizeBytes metrics.Histogram `metrics_bucketsizes:"1,3,7" metrics_buckettype:"exp"`

	// InvalidTxs defines the number of invalid transactions. These are
	// transactions that failed to make it into the mempool because they are
	// deemed invalid.
	// metrics:Number of invalid transactions.
	InvalidTxs metrics.Counter

	// RejectedTxs defines the number of rejected transactions. These are
	// transactions that failed to make it into the mempool due to resource
	// limits, e.g. mempool is full.
	// metrics:Number of rejected transactions.
	RejectedTxs metrics.Counter

	// Number of times transactions are rechecked in the mempool.
	RecheckTimes metrics.Counter

	// EvictedTxs defines the number of evicted transactions. These are valid
	// transactions that passed CheckTx and existed in the mempool but later
	// became invalid.
	// metrics:Number of evicted transactions.
	EvictedTxs metrics.Counter

	// Number of times transactions were received more than once.
	// metrics:Number of duplicate transaction reception.
	AlreadyReceivedTxs metrics.Counter

	// Number of connections being actively used for gossiping transactions
	// (experimental feature).
	ActiveOutboundConnections metrics.Gauge
}
