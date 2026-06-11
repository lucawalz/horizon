package prometheus

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/lucawalz/horizon/internal/k8s"
)

const (
	prometheusServiceName = "kube-prometheus-stack-prometheus"
	prometheusNamespace   = "monitoring"
	prometheusPort        = 9090
	inClusterURLEnv       = "HORIZON_PROMETHEUS_URL"
	defaultInClusterURL   = "http://kube-prometheus-stack-prometheus.monitoring.svc:9090"
)

type Client struct {
	api     v1.API
	baseURL string
	stopCh  chan struct{}
}

func NewClientFromAPI(api v1.API) *Client {
	return &Client{api: api}
}

func NewClient(clientset kubernetes.Interface, kubeconfigPath string) (*Client, error) {
	if kubeconfigPath == "" && k8s.InCluster() {
		return newDirectClient()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	endpoints, err := clientset.CoreV1().Endpoints(prometheusNamespace).Get(
		ctx, prometheusServiceName, metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("prometheus endpoints %s/%s: %w", prometheusNamespace, prometheusServiceName, err)
	}
	if len(endpoints.Subsets) == 0 || len(endpoints.Subsets[0].Addresses) == 0 {
		return nil, fmt.Errorf("prometheus service %s/%s has no ready endpoints", prometheusNamespace, prometheusServiceName)
	}
	targetRef := endpoints.Subsets[0].Addresses[0].TargetRef
	if targetRef == nil || targetRef.Name == "" {
		return nil, fmt.Errorf("prometheus endpoint has no TargetRef pod name")
	}
	podName := targetRef.Name

	restCfg, err := k8s.RestConfig(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig for port-forward: %w", err)
	}

	localPort, err := getFreePort()
	if err != nil {
		return nil, fmt.Errorf("find free port: %w", err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(restCfg)
	if err != nil {
		return nil, fmt.Errorf("spdy round tripper: %w", err)
	}

	pfURL := restCfg.Host + fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", prometheusNamespace, podName)
	req, err := http.NewRequest(http.MethodGet, pfURL, nil)
	if err != nil {
		return nil, fmt.Errorf("port-forward request: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodGet, req.URL)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})

	fw, err := portforward.New(
		dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, prometheusPort)},
		stopCh,
		readyCh,
		io.Discard,
		io.Discard,
	)
	if err != nil {
		close(stopCh)
		return nil, fmt.Errorf("port-forward setup: %w", err)
	}

	go func() {
		if err := fw.ForwardPorts(); err != nil {
			_ = err
		}
	}()

	select {
	case <-readyCh:
	case <-time.After(10 * time.Second):
		close(stopCh)
		return nil, fmt.Errorf("prometheus port-forward: timed out waiting for ready")
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", localPort)
	apiClient, err := promapi.NewClient(promapi.Config{
		Address: baseURL,
	})
	if err != nil {
		close(stopCh)
		return nil, fmt.Errorf("prometheus api client: %w", err)
	}

	return &Client{
		api:     v1.NewAPI(apiClient),
		baseURL: baseURL,
		stopCh:  stopCh,
	}, nil
}

func newDirectClient() (*Client, error) {
	baseURL := os.Getenv(inClusterURLEnv)
	if baseURL == "" {
		baseURL = defaultInClusterURL
	}
	apiClient, err := promapi.NewClient(promapi.Config{Address: baseURL})
	if err != nil {
		return nil, fmt.Errorf("prometheus api client: %w", err)
	}
	return &Client{
		api:     v1.NewAPI(apiClient),
		baseURL: baseURL,
	}, nil
}

func (c *Client) Close() {
	if c.stopCh != nil {
		close(c.stopCh)
		c.stopCh = nil
	}
}

func (c *Client) IsHealthy(ctx context.Context) error {
	if c.baseURL == "" {
		return fmt.Errorf("prometheus: no base URL (client created without port-forward)")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/-/healthy", nil)
	if err != nil {
		return fmt.Errorf("prometheus health request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("prometheus health: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("prometheus health returned %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) QueryInstant(ctx context.Context, query string) (model.Vector, error) {
	result, warnings, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("prometheus query: %w", err)
	}
	// Warnings are non-fatal; partial data is expected when some node-exporters are unreachable.
	_ = warnings

	vec, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected prometheus result type: %T", result)
	}
	return vec, nil
}

func getFreePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
