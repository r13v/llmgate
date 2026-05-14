//go:build e2e

package e2e

func (g *fakeGateway) queueListResponses(responses ...gatewayResponse) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.listResponses = append(g.listResponses, responses...)
}
