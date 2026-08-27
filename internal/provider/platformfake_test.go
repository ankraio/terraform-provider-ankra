// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// writeClusterListing renders the platform's ClusterListResponseContract for
// the clusters a fake currently holds, applying the same server-side
// cluster_id / cluster_name filters the real endpoint applies.
//
// Every fake in this package goes through here. The provider previously
// decoded the listing from a "clusters" key the platform never sends, and each
// hand-written fake repeated that mistake, so the suite passed while the
// provider could not see a cluster. One shared renderer keeps the fakes honest
// about the contract.
func writeClusterListing(clusters map[string]string, query url.Values) string {
	ids := make([]string, 0, len(clusters))
	for id := range clusters {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	rows := make([]string, 0, len(ids))
	for _, id := range ids {
		name := clusters[id]
		if wanted := query.Get("cluster_id"); wanted != "" && wanted != id {
			continue
		}
		if wanted := query.Get("cluster_name"); wanted != "" && wanted != name {
			continue
		}
		rows = append(rows, fmt.Sprintf(
			`{"id":%q,"name":%q,"kind":"hetzner","state":"running"}`, id, name))
	}

	return fmt.Sprintf(
		`{"result":[%s],"pagination":{"total_count":%d,"total_pages":1,"page":1,"page_size":100},"metrics":{}}`,
		strings.Join(rows, ","), len(rows),
	)
}
