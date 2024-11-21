package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cometbft/cometbft/test/e2e/pkg/infra"
)

func InfraCreate(ctx context.Context, infp infra.Provider, ccOnly bool, confirm bool) error {
	startTime := time.Now()
	defer func(t time.Time) { logger.Debug(fmt.Sprintf("Infra create time: %s\n", time.Since(t))) }(startTime)

	err := infp.InfraCreate(ctx, ccOnly, confirm)
	if err != nil {
		return err
	}
	fmt.Printf("\n👉 Do not forget to run 'infra destroy' or 'cleanup' (and type 'yes') when you finish with the testnet!\n")
	return nil
}
