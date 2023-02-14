package ca

import (
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/consul/agent/structs"
)

func NewK8sAuthClient(authMethod *structs.VaultAuthMethod) (*VaultAuthClient, error) {
	params := authMethod.Params
	role, ok := params["role"].(string)
	if !ok || strings.TrimSpace(role) == "" {
		return nil, fmt.Errorf("missing 'role' value")
	}

	authClient := NewVaultAPIAuthClient(authMethod, "")
	authClient.LoginDataGen = K8sLoginDataGen
	return authClient, nil
}

func K8sLoginDataGen(authMethod *structs.VaultAuthMethod) (map[string]any, error) {
	params := authMethod.Params
	role := params["role"].(string)

	// token passed directly
	// refactor this to use the hasJWT function as used in the jwt auth
	if jwt, ok := params["jwt"].(string); ok && strings.TrimSpace(jwt) != "" {
		return map[string]any{
			"role": role,
			"jwt":  jwt,
		}, nil
	}

	// read token from file on path
	tokenPath, ok := params["token_path"].(string)
	if !ok || strings.TrimSpace(tokenPath) == "" {
		tokenPath = defaultK8SServiceAccountTokenPath
	}
	rawToken, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"role": role,
		"jwt":  strings.TrimSpace(string(rawToken)),
	}, nil
}
