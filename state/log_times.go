package state

import (
	"fmt"
	"sort"
	"time"

	"github.com/cometbft/cometbft/types"
)

const logTimeFormat = "2006-01-02|15:04:05.000000" // used by cometbft/libs/log

func formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// Identical to types/time/WeightedTime
// Added a formatted string output.
type WeightedTime struct {
	Time   time.Time
	Weight int64
}

func (wt *WeightedTime) String() string {
	return fmt.Sprintf("(%v, %d)", formatTime(wt.Time), wt.Weight)
}

func (blockExec *BlockExecutor) LogBlockTimestamp(message string, state State, block *types.Block) {
	switch {
	case state.ConsensusParams.Feature.PbtsEnabled(block.Height):
		blockExec.logger.Info("PBTS "+message, "height", block.Height,
			"timestamp", formatTime(block.Time))
	case block.Height == state.InitialHeight:
		blockExec.logger.Info("Genesis-Time "+message, "height",
			block.Height, "timestamp", formatTime(block.Time))
	default:
		timestamps := lastCommmitWeightedTimes(state, block)
		blockExec.logger.Info("BFT-Time "+message, "height", block.Height,
			"timestamp", formatTime(block.Time),
			"precommit_times", timestamps)
	}
}

func lastCommmitWeightedTimes(state State, block *types.Block) (timestamps []*WeightedTime) {
	// Same logic used to compute the WeightedMedian of timestamps
	for _, sign := range block.LastCommit.Signatures {
		if sign.BlockIDFlag == types.BlockIDFlagAbsent {
			continue
		}
		_, validator := state.LastValidators.GetByAddressMut(sign.ValidatorAddress)
		if validator != nil {
			timestamps = append(timestamps, &WeightedTime{
				Time:   sign.Timestamp,
				Weight: validator.VotingPower,
			})
		}
	}
	sort.Slice(timestamps, func(i, j int) bool {
		return timestamps[i].Time.UnixNano() < timestamps[j].Time.UnixNano()
	})
	return timestamps
}
