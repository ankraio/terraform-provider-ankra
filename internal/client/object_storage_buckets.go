// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
)

const objectStorageBucketsPath = "/api/v1/org/object-storage-buckets"

// ObjectStorageBucket is a bucket Ankra created on one of the organisation's
// provider credentials. Key material is never returned.
type ObjectStorageBucket struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Provider     string  `json:"provider"`
	Region       string  `json:"region"`
	Bucket       string  `json:"bucket"`
	Endpoint     string  `json:"endpoint"`
	PathStyle    bool    `json:"path_style"`
	CredentialID string  `json:"credential_id"`
	Status       string  `json:"status"`
	ErrorExcerpt *string `json:"error_excerpt"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

// ObjectStorageBucketCreate asks the platform to create a bucket. The key
// pair is for Hetzner only, and may be left out there when the Hetzner
// credential stores one.
type ObjectStorageBucketCreate struct {
	Name            string  `json:"name"`
	CredentialID    string  `json:"credential_id"`
	Region          string  `json:"region"`
	Bucket          *string `json:"bucket,omitempty"`
	AccessKeyID     *string `json:"access_key_id,omitempty"`
	SecretAccessKey *string `json:"secret_access_key,omitempty"`
}

type objectStorageBucketListEnvelope struct {
	Items []ObjectStorageBucket `json:"items"`
}

// ListObjectStorageBuckets returns the organisation's buckets, newest first.
func (client *Client) ListObjectStorageBuckets(ctx context.Context) ([]ObjectStorageBucket, error) {
	var envelope objectStorageBucketListEnvelope
	if err := client.doRequest(ctx, http.MethodGet, objectStorageBucketsPath, nil, &envelope); err != nil {
		return nil, err
	}
	return envelope.Items, nil
}

// GetObjectStorageBucket reads one bucket. A deleted bucket is a NotFound
// APIError.
func (client *Client) GetObjectStorageBucket(ctx context.Context, bucketID string) (*ObjectStorageBucket, error) {
	var bucket ObjectStorageBucket
	if err := client.doRequest(ctx, http.MethodGet, objectStorageBucketsPath+"/"+url.PathEscape(bucketID), nil, &bucket); err != nil {
		return nil, err
	}
	return &bucket, nil
}

// CreateObjectStorageBucket records the bucket and starts the job that
// creates it. The bucket answers in provisioning.
func (client *Client) CreateObjectStorageBucket(ctx context.Context, request ObjectStorageBucketCreate) (*ObjectStorageBucket, error) {
	var bucket ObjectStorageBucket
	if err := client.doRequest(ctx, http.MethodPost, objectStorageBucketsPath, request, &bucket); err != nil {
		return nil, err
	}
	return &bucket, nil
}

// DeleteObjectStorageBucket stops Ankra managing a bucket. With
// destroyProviderResources the platform also empties and deletes it. A
// bucket the scope cannot see is a NotFound APIError.
func (client *Client) DeleteObjectStorageBucket(ctx context.Context, bucketID string, destroyProviderResources bool) error {
	path := objectStorageBucketsPath + "/" + url.PathEscape(bucketID)
	if destroyProviderResources {
		path += "?destroy_provider_resources=true"
	}
	return client.doRequest(ctx, http.MethodDelete, path, nil, nil)
}
