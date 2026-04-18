// Copyright 2026 the gpuaudit authors. All rights reserved.
// SPDX-License-Identifier: Apache-2.0

package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// Target represents a resolved scan target with its credentials.
type Target struct {
	AccountID string
	Config    aws.Config
}

// TargetError records a target that failed credential resolution.
type TargetError struct {
	AccountID string
	Err       error
}

// STSClient is the subset of the STS API we need.
type STSClient interface {
	GetCallerIdentity(ctx context.Context, params *sts.GetCallerIdentityInput, optFns ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// OrgClient is the subset of the Organizations API we need.
type OrgClient interface {
	ListAccounts(ctx context.Context, params *organizations.ListAccountsInput, optFns ...func(*organizations.Options)) (*organizations.ListAccountsOutput, error)
}

// ResolveTargets determines which accounts to scan and obtains credentials for each.
//
// Behaviour:
//   - No --targets/--org: returns self only (uses baseCfg, no AssumeRole)
//   - --targets + --role: AssumeRole for each, self included by default
//   - --org + --role: ListAccounts, filter Active, AssumeRole for non-self accounts
//   - --skip-self: exclude caller's account
//   - Self account is never AssumeRole'd — uses original credentials
//   - Failed AssumeRole calls are collected as TargetError, not fatal
func ResolveTargets(ctx context.Context, baseCfg aws.Config, stsClient STSClient, orgClient OrgClient, opts ScanOptions) ([]Target, []TargetError) {
	// Identify the caller's own account.
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, []TargetError{{AccountID: "unknown", Err: fmt.Errorf("GetCallerIdentity: %w", err)}}
	}
	selfAccount := aws.ToString(identity.Account)

	// Determine the list of account IDs to scan.
	var accountIDs []string

	switch {
	case opts.OrgScan:
		activeAccounts, listErr := listActiveOrgAccounts(ctx, orgClient)
		if listErr != nil {
			return nil, []TargetError{{AccountID: "org", Err: fmt.Errorf("ListAccounts: %w", listErr)}}
		}
		accountIDs = activeAccounts
	case len(opts.Targets) > 0:
		// Always include self unless it is already in the list or --skip-self is set.
		seen := make(map[string]bool)
		for _, id := range opts.Targets {
			if !seen[id] {
				accountIDs = append(accountIDs, id)
				seen[id] = true
			}
		}
		if !seen[selfAccount] && !opts.SkipSelf {
			// Prepend self so it appears first.
			accountIDs = append([]string{selfAccount}, accountIDs...)
		}
	default:
		// No multi-target flags — scan self only.
		return []Target{{AccountID: selfAccount, Config: baseCfg}}, nil
	}

	// Resolve credentials for each account.
	var targets []Target
	var targetErrors []TargetError

	for _, acctID := range accountIDs {
		if opts.SkipSelf && acctID == selfAccount {
			continue
		}

		if acctID == selfAccount {
			// Self: use original credentials, no AssumeRole.
			targets = append(targets, Target{AccountID: selfAccount, Config: baseCfg})
			continue
		}

		// AssumeRole into the target account.
		cfg, assumeErr := assumeRole(ctx, baseCfg, stsClient, acctID, opts.Role, opts.ExternalID)
		if assumeErr != nil {
			targetErrors = append(targetErrors, TargetError{AccountID: acctID, Err: assumeErr})
			continue
		}
		targets = append(targets, Target{AccountID: acctID, Config: cfg})
	}

	return targets, targetErrors
}

// assumeRole assumes a role in the given account and returns an aws.Config
// with the temporary credentials.
func assumeRole(ctx context.Context, baseCfg aws.Config, stsClient STSClient, accountID, roleName, externalID string) (aws.Config, error) {
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName)

	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleARN),
		RoleSessionName: aws.String("gpuaudit"),
	}
	if externalID != "" {
		input.ExternalId = aws.String(externalID)
	}

	result, err := stsClient.AssumeRole(ctx, input)
	if err != nil {
		return aws.Config{}, fmt.Errorf("AssumeRole %s: %w", roleARN, err)
	}

	creds := result.Credentials
	cfg := baseCfg.Copy()
	cfg.Credentials = credentials.NewStaticCredentialsProvider(
		aws.ToString(creds.AccessKeyId),
		aws.ToString(creds.SecretAccessKey),
		aws.ToString(creds.SessionToken),
	)

	return cfg, nil
}

// listActiveOrgAccounts returns the account IDs of all active accounts in the organization.
func listActiveOrgAccounts(ctx context.Context, orgClient OrgClient) ([]string, error) {
	var accountIDs []string
	var nextToken *string

	for {
		out, err := orgClient.ListAccounts(ctx, &organizations.ListAccountsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}
		for _, acct := range out.Accounts {
			if acct.Status == orgtypes.AccountStatusActive {
				accountIDs = append(accountIDs, aws.ToString(acct.Id))
			}
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	return accountIDs, nil
}
