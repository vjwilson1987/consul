package gateways

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"github.com/hashicorp/consul/api"
	libassert "github.com/hashicorp/consul/test/integration/consul-container/libs/assert"
	libservice "github.com/hashicorp/consul/test/integration/consul-container/libs/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func getNamespace() string {
	return ""
}

// randomName generates a random name of n length with the provided
// prefix. If prefix is omitted, the then entire name is random char.
func randomName(prefix string, n int) string {
	if n == 0 {
		n = 32
	}
	if len(prefix) >= n {
		return prefix
	}
	p := make([]byte, n)
	rand.Read(p)
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(p))[:n]
}

func TestHTTPRouteFlattening(t *testing.T) {
	t.Skip()
	if testing.Short() {
		t.Skip("too slow for testing.Short")
	}

	//TODO currently this test cannot run in paralel because services spinning up
	//at the same time causes the tests to error.
	t.Parallel()

	//infrastructure set up
	listenerPort := 6000
	//create cluster
	cluster := createCluster(t, listenerPort)
	client := cluster.Agents[0].GetClient()
	service1ResponseCode := 200
	service2ResponseCode := 418
	serviceOne := createService(t, cluster, &libservice.ServiceOpts{
		Name:     "service1",
		ID:       "service1",
		HTTPPort: 8080,
		GRPCPort: 8079,
	}, []string{
		//customizes response code so we can distinguish between which service is responding
		"-echo-server-default-params", fmt.Sprintf("status=%d", service1ResponseCode),
	})
	serviceTwo := createService(t, cluster, &libservice.ServiceOpts{
		Name:     "service2",
		ID:       "service2",
		HTTPPort: 8081,
		GRPCPort: 8082,
	}, []string{
		"-echo-server-default-params", fmt.Sprintf("status=%d", service2ResponseCode),
	},
	)

	//TODO this should only matter in consul enterprise I believe?
	namespace := getNamespace()
	gatewayName := randomName("gw", 16)
	routeOneName := randomName("route", 16)
	routeTwoName := randomName("route", 16)
	path1 := "/"
	path2 := "/v2"

	//write config entries
	proxyDefaults := &api.ProxyConfigEntry{
		Kind:      api.ProxyDefaults,
		Name:      api.ProxyConfigGlobal,
		Namespace: namespace,
		Config: map[string]interface{}{
			"protocol": "http",
		},
	}

	_, _, err := client.ConfigEntries().Set(proxyDefaults, nil)
	assert.NoError(t, err)

	apiGateway := &api.APIGatewayConfigEntry{
		Kind: "api-gateway",
		Name: gatewayName,
		Listeners: []api.APIGatewayListener{
			{
				Name:     "listener",
				Port:     listenerPort,
				Protocol: "http",
			},
		},
	}

	routeOne := &api.HTTPRouteConfigEntry{
		Kind: api.HTTPRoute,
		Name: routeOneName,
		Parents: []api.ResourceReference{
			{
				Kind:      api.APIGateway,
				Name:      gatewayName,
				Namespace: namespace,
			},
		},
		Hostnames: []string{
			"test.foo",
			"test.example",
		},
		Namespace: namespace,
		Rules: []api.HTTPRouteRule{
			{
				Services: []api.HTTPService{
					{
						Name:      serviceOne.GetServiceName(),
						Namespace: namespace,
					},
				},
				Matches: []api.HTTPMatch{
					{
						Path: api.HTTPPathMatch{
							Match: api.HTTPPathMatchPrefix,
							Value: path1,
						},
					},
				},
			},
		},
	}

	routeTwo := &api.HTTPRouteConfigEntry{
		Kind: api.HTTPRoute,
		Name: routeTwoName,
		Parents: []api.ResourceReference{
			{
				Kind:      api.APIGateway,
				Name:      gatewayName,
				Namespace: namespace,
			},
		},
		Hostnames: []string{
			"test.foo",
		},
		Namespace: namespace,
		Rules: []api.HTTPRouteRule{
			{
				Services: []api.HTTPService{
					{
						Name:      serviceTwo.GetServiceName(),
						Namespace: namespace,
					},
				},
				Matches: []api.HTTPMatch{
					{
						Path: api.HTTPPathMatch{
							Match: api.HTTPPathMatchPrefix,
							Value: path2,
						},
					},
					{
						Headers: []api.HTTPHeaderMatch{{
							Match: api.HTTPHeaderMatchExact,
							Name:  "x-v2",
							Value: "v2",
						}},
					},
				},
			},
		},
	}

	_, _, err = client.ConfigEntries().Set(apiGateway, nil)
	assert.NoError(t, err)
	_, _, err = client.ConfigEntries().Set(routeOne, nil)
	assert.NoError(t, err)
	_, _, err = client.ConfigEntries().Set(routeTwo, nil)
	assert.NoError(t, err)

	//create gateway service
	gatewayService, err := libservice.NewGatewayService(context.Background(), gatewayName, "api", cluster.Agents[0], listenerPort)
	require.NoError(t, err)
	libassert.CatalogServiceExists(t, client, gatewayName)

	//make sure config entries have been properly created
	checkGatewayConfigEntry(t, client, gatewayName, namespace)
	checkHTTPRouteConfigEntry(t, client, routeOneName, namespace)
	checkHTTPRouteConfigEntry(t, client, routeTwoName, namespace)

	//gateway resolves routes

	ip := "localhost"
	gatewayPort, err := gatewayService.GetPort(listenerPort)
	assert.NoError(t, err)

	//route 2 with headers

	//Same v2 path with and without header
	checkRoute(t, ip, gatewayPort, "v2", map[string]string{
		"Host": "test.foo",
		"x-v2": "v2",
	}, checkOptions{statusCode: service2ResponseCode, testName: "service2 header and path"})
	checkRoute(t, ip, gatewayPort, "v2", map[string]string{
		"Host": "test.foo",
	}, checkOptions{statusCode: service2ResponseCode, testName: "service2 just path match"})

	////v1 path with the header
	checkRoute(t, ip, gatewayPort, "check", map[string]string{
		"Host": "test.foo",
		"x-v2": "v2",
	}, checkOptions{statusCode: service2ResponseCode, testName: "service2 just header match"})

	checkRoute(t, ip, gatewayPort, "v2/path/value", map[string]string{
		"Host": "test.foo",
		"x-v2": "v2",
	}, checkOptions{statusCode: service2ResponseCode, testName: "service2 v2 with path"})

	//hit service 1 by hitting root path
	checkRoute(t, ip, gatewayPort, "", map[string]string{
		"Host": "test.foo",
	}, checkOptions{debug: false, statusCode: service1ResponseCode, testName: "service1 root prefix"})

	//hit service 1 by hitting v2 path with v1 hostname
	checkRoute(t, ip, gatewayPort, "v2", map[string]string{
		"Host": "test.example",
	}, checkOptions{debug: false, statusCode: service1ResponseCode, testName: "service1, v2 path with v2 hostname"})

}

func TestHTTPRoutePathRewrite(t *testing.T) {
	if testing.Short() {
		t.Skip("too slow for testing.Short")
	}

	t.Parallel()

	//infrastructure set up
	listenerPort := 6001
	//create cluster
	cluster := createCluster(t, listenerPort)
	client := cluster.Agents[0].GetClient()
	invalidServiceResponseCode := 400
	validServiceResponseCode := 200
	pathToRewrite := "/v1/api"

	invalidService := createService(t, cluster, &libservice.ServiceOpts{
		Name:     "invalid",
		ID:       "invalid",
		HTTPPort: 8080,
		GRPCPort: 8081,
	}, []string{
		//customizes response code so we can distinguish between which service is responding
		"-echo-server-default-params", fmt.Sprintf("status=%d", invalidServiceResponseCode),
	})
	validService := createService(t, cluster, &libservice.ServiceOpts{
		Name: "valid",
		ID:   "valid",
		//TODO we get conflicts if these ports are the same and the tests are running in parralel
		//need to poll for available ports to avoid conflicts
		HTTPPort: 8079,
		GRPCPort: 8078,
	}, []string{
		"-echo-server-default-params", fmt.Sprintf("status=%d", validServiceResponseCode),
		"-echo-debug-path", pathToRewrite,
	},
	)

	namespace := getNamespace()
	gatewayName := randomName("gw", 16)
	invalidRouteName := randomName("route", 16)
	validRouteName := randomName("route", 16)
	validPath := "/foo"
	invalidPath := "/bar"

	//write config entries
	proxyDefaults := &api.ProxyConfigEntry{
		Kind:      api.ProxyDefaults,
		Name:      api.ProxyConfigGlobal,
		Namespace: namespace,
		Config: map[string]interface{}{
			"protocol": "http",
		},
	}

	_, _, err := client.ConfigEntries().Set(proxyDefaults, nil)
	assert.NoError(t, err)

	apiGateway := createGateway(gatewayName, "http", listenerPort)

	invalidRoute := &api.HTTPRouteConfigEntry{
		Kind: api.HTTPRoute,
		Name: invalidRouteName,
		Parents: []api.ResourceReference{
			{
				Kind:      api.APIGateway,
				Name:      gatewayName,
				Namespace: namespace,
			},
		},
		Hostnames: []string{
			"test.foo",
		},
		Namespace: namespace,
		Rules: []api.HTTPRouteRule{
			{
				Filters: api.HTTPFilters{
					URLRewrite: &api.URLRewrite{
						Path: "/v1/invalid",
					},
				},
				Services: []api.HTTPService{
					{
						Name:      invalidService.GetServiceName(),
						Namespace: namespace,
					},
				},
				Matches: []api.HTTPMatch{
					{
						Path: api.HTTPPathMatch{
							Match: api.HTTPPathMatchPrefix,
							Value: "/bar",
						},
					},
				},
			},
		},
	}

	validRoute := &api.HTTPRouteConfigEntry{
		Kind: api.HTTPRoute,
		Name: validRouteName,
		Parents: []api.ResourceReference{
			{
				Kind:      api.APIGateway,
				Name:      gatewayName,
				Namespace: namespace,
			},
		},
		Hostnames: []string{
			"test.foo",
		},
		Namespace: namespace,
		Rules: []api.HTTPRouteRule{
			{
				Filters: api.HTTPFilters{
					URLRewrite: &api.URLRewrite{
						Path: "/v1/api",
					},
				},
				Services: []api.HTTPService{
					{
						Name:      validService.GetServiceName(),
						Namespace: namespace,
					},
				},
				Matches: []api.HTTPMatch{
					{
						Path: api.HTTPPathMatch{
							Match: api.HTTPPathMatchPrefix,
							Value: "/foo",
						},
					},
				},
			},
		},
	}

	_, _, err = client.ConfigEntries().Set(apiGateway, nil)
	assert.NoError(t, err)
	_, _, err = client.ConfigEntries().Set(invalidRoute, nil)
	assert.NoError(t, err)
	_, _, err = client.ConfigEntries().Set(validRoute, nil)
	assert.NoError(t, err)

	//create gateway service
	gatewayService, err := libservice.NewGatewayService(context.Background(), gatewayName, "api", cluster.Agents[0], listenerPort)
	require.NoError(t, err)
	libassert.CatalogServiceExists(t, client, gatewayName)

	//make sure config entries have been properly created
	checkGatewayConfigEntry(t, client, gatewayName, namespace)
	checkHTTPRouteConfigEntry(t, client, invalidRouteName, namespace)
	checkHTTPRouteConfigEntry(t, client, validRouteName, namespace)

	ip := "localhost"
	gatewayPort, err := gatewayService.GetPort(listenerPort)
	assert.NoError(t, err)

	//TODO these were the assertions we had in the original test, but I'm unclear on why invalid is invalid

	//hit invalid service by hitting root path
	checkRoute(t, ip, gatewayPort, invalidPath, map[string]string{
		"Host": "test.foo",
	}, checkOptions{debug: false, statusCode: invalidServiceResponseCode, testName: "invlaid service"})

	//hit valid path, double check that the response body returns properly
	checkRoute(t, ip, gatewayPort, validPath, map[string]string{
		"Host": "test.foo",
	}, checkOptions{debug: true, statusCode: validServiceResponseCode, testName: "valid service"})

}
