package capi

import (
	"bytes"
	"context"
	"fmt"
	"io"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const yamlDecodeBuffer = 4096

func DecodeManifests(data []byte) ([]*unstructured.Unstructured, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), yamlDecodeBuffer)
	var objs []*unstructured.Unstructured
	for {
		raw := map[string]any{}
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("capi: decode manifest: %w", err)
		}
		if len(raw) == 0 {
			continue
		}
		objs = append(objs, &unstructured.Unstructured{Object: raw})
	}
	return objs, nil
}

func (c *Client) ApplyManifests(ctx context.Context, data []byte) error {
	objs, err := DecodeManifests(data)
	if err != nil {
		return err
	}
	for _, obj := range objs {
		if err := c.applyObject(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) applyObject(ctx context.Context, obj *unstructured.Unstructured) error {
	existing := obj.DeepCopy()
	err := c.crClient().Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if apierrors.IsNotFound(err) {
		if err := c.crClient().Create(ctx, obj); err != nil {
			return fmt.Errorf("capi: create %s %q: %w", obj.GetKind(), obj.GetName(), err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("capi: get %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}
	obj.SetResourceVersion(existing.GetResourceVersion())
	if err := c.crClient().Update(ctx, obj); err != nil {
		return fmt.Errorf("capi: update %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}
	return nil
}
