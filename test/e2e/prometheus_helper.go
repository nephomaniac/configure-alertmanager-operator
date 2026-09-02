//go:build osde2e
// +build osde2e

package osde2etests

import (
	"context"
	"errors"
	"fmt"
	"time"

	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/library-go/test/library/metrics"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type prometheusClient struct {
	api prometheusv1.API
}

func newPrometheusClient(ctx context.Context, cfg *rest.Config) (*prometheusClient, error) {
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	routeClient, err := routev1client.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create route client: %w", err)
	}

	promAPI, err := metrics.NewPrometheusClient(ctx, kubeClient, routeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}

	return &prometheusClient{api: promAPI}, nil
}

func (c *prometheusClient) InstantQuery(ctx context.Context, query string) (model.Vector, error) {
	result, _, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, errors.New("failed to convert result to a Vector object")
	}

	return vector, nil
}
