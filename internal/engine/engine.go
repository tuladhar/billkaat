// Package engine runs a scan: it fans checks out across a small worker pool
// and persists progress so the UI can poll.
package engine

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/billkaat/billkaat/internal/awsx"
	"github.com/billkaat/billkaat/internal/checks"
	"github.com/billkaat/billkaat/internal/store"
)

type Engine struct {
	Store   *store.Store
	Workers int
}

// StartScan creates the scan row and launches the run in the background.
func (e *Engine) StartScan(region string) (int64, error) {
	id, err := e.Store.CreateScan(region)
	if err != nil {
		return 0, err
	}
	go e.run(id, region)
	return id, nil
}

func (e *Engine) run(scanID int64, region string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	all := checks.All()
	for _, c := range all {
		st := "pending"
		if c.Meta().Locked {
			st = "locked"
		}
		_ = e.Store.InitCheck(scanID, c.Meta().ID, st)
	}

	clients, err := awsx.New(ctx, region)
	if err != nil {
		_ = e.Store.FailScan(scanID, friendlyAWSError(err))
		return
	}
	ident, err := clients.Identity(ctx)
	if err != nil {
		_ = e.Store.FailScan(scanID, friendlyAWSError(err))
		return
	}
	_ = e.Store.SetScanAccount(scanID, ident.Account)

	rc := &checks.RunContext{Ctx: ctx, AWS: clients, Region: clients.Region}

	var (
		mu            sync.Mutex
		totalSavings  float64
		totalFindings int
	)

	workers := e.Workers
	if workers < 1 {
		workers = 4
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, c := range all {
		if c.Meta().Locked {
			continue
		}
		c := c
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			meta := c.Meta()
			_ = e.Store.SetCheckStatus(scanID, meta.ID, "running", "", 0, 0, 0)

			start := time.Now()
			findings, err := runSafely(c, rc)
			durMs := time.Since(start).Milliseconds()

			if err != nil {
				_ = e.Store.SetCheckStatus(scanID, meta.ID, "error",
					friendlyAWSError(err), 0, 0, durMs)
				return
			}

			var savings float64
			for i := range findings {
				findings[i].CheckID = meta.ID
				if findings[i].Region == "" {
					findings[i].Region = clients.Region
				}
				savings += findings[i].MonthlySavingsUSD
			}
			if len(findings) > 0 {
				_ = e.Store.AddFindings(scanID, findings)
			}
			status := "passed"
			if len(findings) > 0 {
				status = "flagged"
			}
			_ = e.Store.SetCheckStatus(scanID, meta.ID, status, "",
				len(findings), savings, durMs)

			mu.Lock()
			totalSavings += savings
			totalFindings += len(findings)
			mu.Unlock()
		}()
	}
	wg.Wait()
	_ = e.Store.FinishScan(scanID, totalSavings, totalFindings)
}

// runSafely converts a panicking check into an error so one bad check can
// never take the whole scan down.
func runSafely(c checks.Check, rc *checks.RunContext) (fs []checks.Finding, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("check panicked: %v", r)
		}
	}()
	return c.Run(rc)
}

// friendlyAWSError adds a plain-language hint to the most common failures.
func friendlyAWSError(err error) string {
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "no ec2 imds role found"),
		strings.Contains(lower, "failed to retrieve credentials"),
		strings.Contains(lower, "static credentials are empty"),
		strings.Contains(lower, "get identity: get credentials"):
		return msg + " — no AWS credentials found. Set AWS_ACCESS_KEY_ID and " +
			"AWS_SECRET_ACCESS_KEY (or configure ~/.aws/credentials), using an IAM " +
			"user with the read-only policy from iam-policy.json."
	case strings.Contains(lower, "unauthorizedoperation"),
		strings.Contains(lower, "accessdenied"):
		return msg + " — the credentials work but are missing a read permission. " +
			"Attach the read-only policy from iam-policy.json."
	case strings.Contains(lower, "invalidclienttokenid"),
		strings.Contains(lower, "signaturedoesnotmatch"):
		return msg + " — the access key or secret looks wrong. Double-check the values."
	}
	return msg
}
