// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"ankra.io/terraform-provider-ankra/internal/client"
)

// managedPoolListErrorPatience is how many pool listings in a row may fail
// while a replacement is followed before the wait gives up.
const managedPoolListErrorPatience = 4

// apiStatus is the HTTP status of a platform error, or 0.
func apiStatus(err error) int {
	var apiError *client.APIError
	if errors.As(err, &apiError) {
		return apiError.StatusCode
	}
	return 0
}

// errManagedPoolReplaceUnsupported is the answer of a platform, or a
// provider lane, without the replace route.
var errManagedPoolReplaceUnsupported = errors.New("this Ankra platform does not support changing a managed node pool's size " +
	"for this provider: the replace route is missing. Upgrade the platform, or change the size by replacing the resource " +
	"(terraform apply -replace=<address>)")

// managedPoolReplacement follows one size change of a pool: the pool current
// is replaced by replacementName of the given size.
type managedPoolReplacement struct {
	apiClient       *client.Client
	provider        string
	clusterID       string
	name            string
	current         string
	replacementName string
	size            string
	// operationID is the platform operation being followed, when known.
	operationID string
	// isFinished records a replacement the platform had already finished.
	isFinished bool
	// isExecutionHidden records that the operation cannot be read, so only
	// the pool listing is followed.
	isExecutionHidden bool
	diagnostics       *diag.Diagnostics
}

// replaceManagedPool replaces the pool current with one named
// replacementName of the given size and waits until the platform has
// finished: the replacement is listed and the replaced pool is gone. A
// replacement already running for the same change is followed, and one the
// platform already finished is accepted. Warnings go to diagnostics; the
// returned error is the failure.
func replaceManagedPool(ctx context.Context, apiClient *client.Client, provider, clusterID, name, current, replacementName, size string, diagnostics *diag.Diagnostics) error {
	replacement := &managedPoolReplacement{
		apiClient: apiClient, provider: provider, clusterID: clusterID, name: name,
		current: current, replacementName: replacementName, size: size, diagnostics: diagnostics,
	}
	if err := replacement.request(ctx); err != nil || replacement.isFinished {
		return err
	}
	return replacement.await(ctx)
}

// request asks the platform for the replacement. A 202 records the
// operation to follow; a 409 "already in progress" leaves it unknown, so the
// replacement is followed through the listing and asked for again until the
// platform accepts it (once the running attempt has failed) or it finishes.
// A 404 or 405 while the pool is still listed means the route is missing.
func (replacement *managedPoolReplacement) request(ctx context.Context) error {
	accepted, err := replacement.apiClient.ReplaceManagedNodePool(ctx, replacement.provider, replacement.clusterID, replacement.current,
		client.ManagedNodePoolReplace{Size: replacement.size, ReplacementPoolName: &replacement.replacementName})
	switch {
	case err == nil:
		replacement.operationID = accepted.OperationID
		return nil
	case isManagedPoolReplacementInProgress(err):
		return nil
	case apiStatus(err) == http.StatusMethodNotAllowed:
		return errManagedPoolReplaceUnsupported
	case client.IsNotFound(err):
		pools, listError := replacement.apiClient.ListManagedNodePools(ctx, replacement.provider, replacement.clusterID)
		if listError != nil {
			return err
		}
		for _, pool := range pools {
			if pool.Name == replacement.replacementName && strings.EqualFold(pool.Size, replacement.size) {
				replacement.isFinished = true
				return nil
			}
		}
		for _, pool := range pools {
			if pool.Name == replacement.current {
				return errManagedPoolReplaceUnsupported
			}
		}
		return err
	default:
		return err
	}
}

// await polls the pool listing, and the operation when it is known, until
// the replacement finishes, fails, or ctx ends.
func (replacement *managedPoolReplacement) await(ctx context.Context) error {
	failedListings := 0
	for {
		pools, listError := replacement.apiClient.ListManagedNodePools(ctx, replacement.provider, replacement.clusterID)
		switch {
		case listError == nil:
			failedListings = 0
			isFinished, err := replacement.judgeListing(ctx, pools)
			if err != nil || isFinished {
				return err
			}
		case ctx.Err() == nil:
			failedListings++
			if failedListings >= managedPoolListErrorPatience {
				return fmt.Errorf("the pool listing failed %d times in a row while following the replacement of pool %s by %s: %w",
					failedListings, replacement.current, replacement.replacementName, listError)
			}
		}
		if replacement.operationID != "" && !replacement.isExecutionHidden {
			isFinished, err := replacement.judgeOperation(ctx, listError == nil)
			if err != nil || isFinished {
				return err
			}
		}
		select {
		case <-ctx.Done():
			operation := ""
			if replacement.operationID != "" {
				operation = " as operation " + replacement.operationID
			}
			return fmt.Errorf("the replacement of pool %s by %s was accepted but had not finished before the update timeout; "+
				"it carries on at the platform%s", replacement.current, replacement.replacementName, operation)
		case <-time.After(managedPoolPollInterval):
		}
	}
}

// judgeListing reads one pool listing: finished once the replacement is
// listed and the replaced pool is gone. While a replacement whose operation
// is unknown is followed, a pool with another size's derived name means
// another size change is running, and otherwise the replacement is asked for
// again, so a failed attempt is retried rather than waited on.
func (replacement *managedPoolReplacement) judgeListing(ctx context.Context, pools []client.ManagedNodePool) (bool, error) {
	isCurrentListed, isReplacementListed := false, false
	for _, pool := range pools {
		isCurrentListed = isCurrentListed || pool.Name == replacement.current
		isReplacementListed = isReplacementListed || pool.Name == replacement.replacementName
	}
	if isReplacementListed && !isCurrentListed {
		return true, nil
	}
	if !isCurrentListed {
		return false, fmt.Errorf("pool %s is no longer listed and its replacement %s is not listed either; "+
			"see `ankra cluster operations list`, and refresh before applying again", replacement.current, replacement.replacementName)
	}
	if replacement.operationID != "" {
		return false, nil
	}
	for _, pool := range pools {
		if pool.Name == replacement.current || pool.Name == replacement.replacementName {
			continue
		}
		if pool.Name == managedReplacementPoolName(replacement.name, pool.Size) && !strings.EqualFold(pool.Size, replacement.size) {
			return false, fmt.Errorf("a replacement of pool %s to size %s (pool %s) is already running; wait for it to finish, "+
				"or set size back to %s", replacement.current, pool.Size, pool.Name, pool.Size)
		}
	}
	if err := replacement.request(ctx); err != nil || replacement.isFinished {
		return replacement.isFinished, err
	}
	return false, nil
}

// judgeOperation reads the followed operation. An operation the token cannot
// read (401, 403 or 404) is reported once, and only the listing is followed
// from then on.
func (replacement *managedPoolReplacement) judgeOperation(ctx context.Context, isListingCurrent bool) (bool, error) {
	execution, err := replacement.apiClient.GetExecution(ctx, replacement.operationID)
	if err != nil {
		switch status := apiStatus(err); status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			replacement.isExecutionHidden = true
			replacement.diagnostics.AddWarning("Replacement operation cannot be followed",
				fmt.Sprintf("The platform answered %d for operation %s, so the replacement of pool %s by %s is followed through "+
					"the pool listing only, and a failed attempt is noticed at the update timeout.",
					status, replacement.operationID, replacement.current, replacement.replacementName))
		}
		return false, nil
	}
	if execution.Status == client.ExecutionStatusSuccess && isListingCurrent {
		return true, nil
	}
	if client.IsExecutionFailed(execution.Status) {
		detail := ""
		if execution.ErrorExcerpt != nil && *execution.ErrorExcerpt != "" {
			detail = ": " + *execution.ErrorExcerpt
		}
		return false, fmt.Errorf("the replacement of pool %s by %s ended %s%s", replacement.current, replacement.replacementName, execution.Status, detail)
	}
	return false, nil
}

// deleteManagedPoolReplacements deletes the pools a size change of this
// resource created that are still listed beside the current one, so a
// replacement still running when the resource is destroyed is not left
// behind.
func deleteManagedPoolReplacements(ctx context.Context, apiClient *client.Client, provider, clusterID, name, current string) error {
	pools, err := apiClient.ListManagedNodePools(ctx, provider, clusterID)
	if err != nil {
		if client.IsNotFound(err) {
			return nil
		}
		return err
	}
	for _, pool := range pools {
		if pool.Name == current || pool.Name != managedReplacementPoolName(name, pool.Size) {
			continue
		}
		if deleteError := apiClient.DeleteManagedNodePool(ctx, provider, clusterID, pool.Name); deleteError != nil && !client.IsNotFound(deleteError) {
			return fmt.Errorf("deleting replacement pool %s: %w", pool.Name, deleteError)
		}
	}
	return nil
}
