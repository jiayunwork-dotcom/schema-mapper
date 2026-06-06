package registry

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/parser"
)

const (
	RegistryDirName = ".schema-registry"
	ConfigFileName  = "config.yaml"
	VersionsDirName = "versions"
)

type RegistryConfig struct {
	Version    string   `yaml:"version"`
	SchemaList []string `yaml:"schemas"`
}

type Registry struct {
	RootDir string
	Config  *RegistryConfig
}

func NewRegistry(rootDir string) *Registry {
	return &Registry{
		RootDir: rootDir,
		Config: &RegistryConfig{
			Version:    "1.0",
			SchemaList: make([]string, 0),
		},
	}
}

func FindRegistry(startDir string) (*Registry, error) {
	dir, err := filepath.Abs(startDir)
	if err != nil {
		return nil, err
	}

	for {
		registryPath := filepath.Join(dir, RegistryDirName)
		if info, err := os.Stat(registryPath); err == nil && info.IsDir() {
			reg := NewRegistry(dir)
			if err := reg.LoadConfig(); err != nil {
				return nil, err
			}
			return reg, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return nil, fmt.Errorf("schema registry not found (no .schema-registry directory found in path)")
}

func (r *Registry) RegistryPath() string {
	return filepath.Join(r.RootDir, RegistryDirName)
}

func (r *Registry) ConfigPath() string {
	return filepath.Join(r.RegistryPath(), ConfigFileName)
}

func (r *Registry) VersionsPath() string {
	return filepath.Join(r.RegistryPath(), VersionsDirName)
}

func (r *Registry) SchemaVersionsPath(schemaName string) string {
	return filepath.Join(r.VersionsPath(), schemaName)
}

func (r *Registry) SchemaVersionPath(schemaName, version string) string {
	return filepath.Join(r.SchemaVersionsPath(schemaName), version+".yaml")
}

func (r *Registry) Init() error {
	registryPath := r.RegistryPath()
	if _, err := os.Stat(registryPath); err == nil {
		return fmt.Errorf("schema registry already exists at: %s", registryPath)
	}

	if err := os.MkdirAll(registryPath, 0755); err != nil {
		return fmt.Errorf("failed to create registry directory: %w", err)
	}

	if err := os.MkdirAll(r.VersionsPath(), 0755); err != nil {
		return fmt.Errorf("failed to create versions directory: %w", err)
	}

	if err := r.SaveConfig(); err != nil {
		return err
	}

	return nil
}

func (r *Registry) LoadConfig() error {
	data, err := ioutil.ReadFile(r.ConfigPath())
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	var config RegistryConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}
	r.Config = &config
	return nil
}

func (r *Registry) SaveConfig() error {
	data, err := yaml.Marshal(r.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	if err := ioutil.WriteFile(r.ConfigPath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}
	return nil
}

func (r *Registry) AddSchemaVersion(schemaName, version string, schemaFile string) error {
	parsedVersion, err := ParseSemVer(version)
	if err != nil {
		return err
	}

	existingVersions, err := r.GetSchemaVersions(schemaName)
	if err != nil {
		return err
	}

	for _, v := range existingVersions {
		if v.Equal(parsedVersion) {
			return fmt.Errorf("version %s already exists for schema %s", version, schemaName)
		}
	}

	if len(existingVersions) > 0 {
		sort.Sort(SemVerList(existingVersions))
		latest := existingVersions[0]
		if !parsedVersion.GreaterThan(latest) {
			return fmt.Errorf("new version %s must be greater than latest version %s", version, latest.String())
		}
	}

	registry := parser.NewParserRegistry()
	schema, err := registry.ParseFile(schemaFile, "")
	if err != nil {
		return fmt.Errorf("failed to parse schema file: %w", err)
	}
	schema.Version = version

	schemaPath := r.SchemaVersionsPath(schemaName)
	if err := os.MkdirAll(schemaPath, 0755); err != nil {
		return fmt.Errorf("failed to create schema directory: %w", err)
	}

	yamlData, err := yaml.Marshal(schema)
	if err != nil {
		return fmt.Errorf("failed to marshal schema: %w", err)
	}

	versionPath := r.SchemaVersionPath(schemaName, parsedVersion.String())
	if err := ioutil.WriteFile(versionPath, yamlData, 0644); err != nil {
		return fmt.Errorf("failed to write schema version: %w", err)
	}

	found := false
	for _, s := range r.Config.SchemaList {
		if s == schemaName {
			found = true
			break
		}
	}
	if !found {
		r.Config.SchemaList = append(r.Config.SchemaList, schemaName)
		sort.Strings(r.Config.SchemaList)
		if err := r.SaveConfig(); err != nil {
			return err
		}
	}

	return nil
}

func (r *Registry) GetSchemaVersions(schemaName string) ([]*SemVer, error) {
	schemaPath := r.SchemaVersionsPath(schemaName)
	if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
		return []*SemVer{}, nil
	}

	files, err := ioutil.ReadDir(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema versions: %w", err)
	}

	versions := make([]*SemVer, 0)
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		name := f.Name()
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		versionStr := name[:len(name)-len(".yaml")]
		v, err := ParseSemVer(versionStr)
		if err != nil {
			continue
		}
		versions = append(versions, v)
	}

	return versions, nil
}

func (r *Registry) ListSchemas() ([]string, error) {
	return r.Config.SchemaList, nil
}

func (r *Registry) GetSchema(schemaName, version string) (*ir.Schema, error) {
	_, err := ParseSemVer(version)
	if err != nil {
		return nil, err
	}

	versionPath := r.SchemaVersionPath(schemaName, version)
	data, err := ioutil.ReadFile(versionPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema version %s/%s: %w", schemaName, version, err)
	}

	var schema ir.Schema
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("failed to parse schema: %w", err)
	}

	return &schema, nil
}

func (r *Registry) GetAllSchemas() (map[string][]*SemVer, error) {
	result := make(map[string][]*SemVer)
	for _, schemaName := range r.Config.SchemaList {
		versions, err := r.GetSchemaVersions(schemaName)
		if err != nil {
			return nil, err
		}
		sort.Sort(SemVerList(versions))
		result[schemaName] = versions
	}
	return result, nil
}

func (r *Registry) GetVersionsBetween(schemaName, fromV, toV string) ([]*SemVer, error) {
	from, err := ParseSemVer(fromV)
	if err != nil {
		return nil, err
	}
	to, err := ParseSemVer(toV)
	if err != nil {
		return nil, err
	}

	allVersions, err := r.GetSchemaVersions(schemaName)
	if err != nil {
		return nil, err
	}

	result := make([]*SemVer, 0)
	for _, v := range allVersions {
		if v.GreaterThan(from) && v.LessThan(to) {
			result = append(result, v)
		}
	}

	sort.Sort(SemVerList(result))
	sort.Slice(result, func(i, j int) bool {
		return result[i].LessThan(result[j])
	})

	return result, nil
}

func SchemaToYAML(s *ir.Schema) (string, error) {
	b, err := yaml.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func SchemaToJSON(s *ir.Schema) (string, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
