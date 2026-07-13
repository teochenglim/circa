package auth

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// SetUser bcrypt-hashes password and writes/overwrites auth.users[username]
// in the YAML file at configPath, in place — this is what `circa auth
// add-user`/`reset-password` (DESIGN/08 §8.1.2) do under the hood. It edits
// the parsed yaml.Node tree rather than round-tripping through Config, so
// comments and formatting elsewhere in the file survive.
func SetUser(configPath, username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading %s: %w", configPath, err)
	}

	var doc yaml.Node
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parsing %s: %w", configPath, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]

	authNode := mustMappingChild(root, "auth")
	usersNode := mustMappingChild(authNode, "users")
	setMappingValue(usersNode, username, hash)

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", configPath, err)
	}
	if err := os.WriteFile(configPath, out, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", configPath, err)
	}
	return nil
}

// mustMappingChild returns the mapping-valued child of key under a mapping
// node, creating it (as an empty mapping) if absent.
func mustMappingChild(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valNode)
	return valNode
}

// setMappingValue upserts key: value (both scalars) into a mapping node.
func setMappingValue(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].SetString(value)
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
	mapping.Content = append(mapping.Content, keyNode, valNode)
}
