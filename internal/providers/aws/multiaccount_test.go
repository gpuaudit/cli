// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	ststypes "github.com/aws/aws-sdk-go-v2/service/sts/types"
)

// --- Mock STS client ---

type mockSTSClient struct {
	callerAccount string
	assumeResults map[string]*sts.AssumeRoleOutput // accountID -> output
	assumeErrors  map[string]error                 // accountID -> error
}

func (m *mockSTSClient) GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return &sts.GetCallerIdentityOutput{
		Account: aws.String(m.callerAccount),
	}, nil
}

func (m *mockSTSClient) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	// Extract account ID from the role ARN: arn:aws:iam::<account>:role/<name>
	arn := aws.ToString(params.RoleArn)
	// Simple parse: find the account between the 4th and 5th colons
	accountID := parseAccountFromARN(arn)

	if err, ok := m.assumeErrors[accountID]; ok {
		return nil, err
	}
	if out, ok := m.assumeResults[accountID]; ok {
		return out, nil
	}
	return nil, fmt.Errorf("no mock configured for account %s", accountID)
}

func parseAccountFromARN(arn string) string {
	// arn:aws:iam::123456789012:role/name
	colons := 0
	start := 0
	for i, c := range arn {
		if c == ':' {
			colons++
			if colons == 4 {
				start = i + 1
			}
			if colons == 5 {
				return arn[start:i]
			}
		}
	}
	return ""
}

// --- Mock Org client ---

type mockOrgClient struct {
	accounts []orgtypes.Account
	err      error
}

func (m *mockOrgClient) ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &organizations.ListAccountsOutput{Accounts: m.accounts}, nil
}

// Helper to build a successful AssumeRole result with dummy credentials.
func assumeRoleOK(accountID string) *sts.AssumeRoleOutput {
	exp := time.Now().Add(1 * time.Hour)
	return &sts.AssumeRoleOutput{
		Credentials: &ststypes.Credentials{
			AccessKeyId:     aws.String("AKID-" + accountID),
			SecretAccessKey: aws.String("SECRET-" + accountID),
			SessionToken:    aws.String("TOKEN-" + accountID),
			Expiration:      &exp,
		},
	}
}

func TestResolveTargets_NoTargets_ReturnsSelfOnly(t *testing.T) {
	stsClient := &mockSTSClient{callerAccount: "111111111111"}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{}

	self, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs)
	}
	if self != "111111111111" {
		t.Errorf("expected self account 111111111111, got %s", self)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (self), got %d", len(targets))
	}
	if targets[0].AccountID != "111111111111" {
		t.Errorf("expected account 111111111111, got %s", targets[0].AccountID)
	}
}

func TestResolveTargets_ExplicitTargets_ReturnsSelfPlusAssumed(t *testing.T) {
	stsClient := &mockSTSClient{
		callerAccount: "111111111111",
		assumeResults: map[string]*sts.AssumeRoleOutput{
			"222222222222": assumeRoleOK("222222222222"),
			"333333333333": assumeRoleOK("333333333333"),
		},
	}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{
		Targets: []string{"222222222222", "333333333333"},
		Role:    "AuditRole",
	}

	_, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
	// Self + 2 explicit targets = 3
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets, got %d", len(targets))
	}

	// Verify self is included
	found := false
	for _, tgt := range targets {
		if tgt.AccountID == "111111111111" {
			found = true
			break
		}
	}
	if !found {
		t.Error("self account 111111111111 not found in targets")
	}

	// Verify assumed targets
	for _, acct := range []string{"222222222222", "333333333333"} {
		found := false
		for _, tgt := range targets {
			if tgt.AccountID == acct {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("account %s not found in targets", acct)
		}
	}
}

func TestResolveTargets_ExplicitTargets_SkipSelf(t *testing.T) {
	stsClient := &mockSTSClient{
		callerAccount: "111111111111",
		assumeResults: map[string]*sts.AssumeRoleOutput{
			"222222222222": assumeRoleOK("222222222222"),
		},
	}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{
		Targets:  []string{"222222222222"},
		Role:     "AuditRole",
		SkipSelf: true,
	}

	self, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d", len(errs))
	}
	if self != "111111111111" {
		t.Errorf("expected self account 111111111111, got %s", self)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target (no self), got %d", len(targets))
	}
	if targets[0].AccountID != "222222222222" {
		t.Errorf("expected account 222222222222, got %s", targets[0].AccountID)
	}
}

func TestResolveTargets_PartialFailure(t *testing.T) {
	stsClient := &mockSTSClient{
		callerAccount: "111111111111",
		assumeResults: map[string]*sts.AssumeRoleOutput{
			"222222222222": assumeRoleOK("222222222222"),
		},
		assumeErrors: map[string]error{
			"333333333333": fmt.Errorf("access denied"),
		},
	}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{
		Targets: []string{"222222222222", "333333333333"},
		Role:    "AuditRole",
	}

	_, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, nil, opts)

	// Self + 222 succeeded, 333 failed
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0].AccountID != "333333333333" {
		t.Errorf("expected error for 333333333333, got %s", errs[0].AccountID)
	}
}

func TestResolveTargets_OrgDiscovery(t *testing.T) {
	stsClient := &mockSTSClient{
		callerAccount: "111111111111",
		assumeResults: map[string]*sts.AssumeRoleOutput{
			"222222222222": assumeRoleOK("222222222222"),
			"444444444444": assumeRoleOK("444444444444"),
		},
	}
	orgClient := &mockOrgClient{
		accounts: []orgtypes.Account{
			{Id: aws.String("111111111111"), Status: orgtypes.AccountStatusActive},
			{Id: aws.String("222222222222"), Status: orgtypes.AccountStatusActive},
			{Id: aws.String("333333333333"), Status: orgtypes.AccountStatusSuspended},
			{Id: aws.String("444444444444"), Status: orgtypes.AccountStatusActive},
		},
	}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{
		OrgScan: true,
		Role:    "AuditRole",
	}

	_, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, orgClient, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs)
	}
	// Active accounts: 111 (self), 222, 444. Suspended 333 is filtered.
	if len(targets) != 3 {
		t.Fatalf("expected 3 targets (self + 2 active non-self), got %d", len(targets))
	}

	// Verify suspended account is excluded
	for _, tgt := range targets {
		if tgt.AccountID == "333333333333" {
			t.Error("suspended account 333333333333 should be excluded")
		}
	}

	// Verify self is included
	found := false
	for _, tgt := range targets {
		if tgt.AccountID == "111111111111" {
			found = true
			break
		}
	}
	if !found {
		t.Error("self account 111111111111 not found in targets")
	}
}

func TestResolveTargets_SelfInExplicitTargets_NotAssumed(t *testing.T) {
	// If the caller's own account appears in --targets, it should use baseCfg (no AssumeRole).
	stsClient := &mockSTSClient{
		callerAccount: "111111111111",
		assumeResults: map[string]*sts.AssumeRoleOutput{
			"222222222222": assumeRoleOK("222222222222"),
		},
		// No AssumeRole result for self — it should not be called
		assumeErrors: map[string]error{
			"111111111111": fmt.Errorf("should not assume role for self"),
		},
	}
	baseCfg := aws.Config{Region: "us-east-1"}
	opts := ScanOptions{
		Targets: []string{"111111111111", "222222222222"},
		Role:    "AuditRole",
	}

	_, targets, errs := ResolveTargets(context.Background(), baseCfg, stsClient, nil, opts)

	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %d: %v", len(errs), errs)
	}
	// Self (from targets list, no duplicate) + 222
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}
