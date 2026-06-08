package velero

import "k8s.io/client-go/util/flowcontrol"

func ClientRateLimiterForTest(c *Client) flowcontrol.RateLimiter {
	if c == nil || c.restCfg == nil {
		return nil
	}
	return c.restCfg.RateLimiter
}
