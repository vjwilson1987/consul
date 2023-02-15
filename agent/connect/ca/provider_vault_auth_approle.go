package ca

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/consul/agent/structs"
)

// left out 2 config options as we are re-using vault agent's auth config.
// Why?
// remove_secret_id_file_after_reading - don't remove what we don't own
// secret_id_response_wrapping_path - wrapping the secret before writing to disk
//                                    (which we don't need to do)

func NewAppRoleAuthClient(authMethod *structs.VaultAuthMethod) (*VaultAuthClient, error) {
	params := authMethod.Params
	client := NewVaultAPIAuthClient(authMethod, "")
	// handle legacy case where role_id and secret_id are passed in directly.
	_, role_id_ok := params["role_id"].(string)
	_, secret_id_ok := params["secret_id"].(string)
	if role_id_ok && secret_id_ok {
		return client, nil
	}

	// vault-agent auth config, role_id_file_path is required
	key := "role_id_file_path"
	if val, ok := authMethod.Params[key].(string); !ok {
		return nil, fmt.Errorf("missing '%s' value", key)
	} else if strings.TrimSpace(val) == "" {
		return nil, fmt.Errorf("'%s' value is empty", key)
	}
	client.LoginDataGen = ArLoginDataGen

	return client, nil
}

// don't need to check for legacy params as this func isn't used in that case
func ArLoginDataGen(authMethod *structs.VaultAuthMethod) (map[string]any, error) {
	params := authMethod.Params
	// role_id is required
	roleIdFilePath := params["role_id_file_path"].(string)
	// secret_id is optional (secret_ok is used in check below)
	secretIdFilePath, secret_ok := params["secret_id_file_path"].(string)

	var err error
	var rawRoleID, rawSecretID []byte
	data := make(map[string]any)
	if rawRoleID, err = os.ReadFile(roleIdFilePath); err != nil {
		return nil, err
	}
	data["role_id"] = string(rawRoleID)
	switch rawSecretID, err = os.ReadFile(secretIdFilePath); {
	case err != nil && secret_ok:
		return nil, err
	case len(bytes.TrimSpace(rawSecretID)) > 0:
		data["secret_id"] = string(rawSecretID)
	}

	return data, nil
}
