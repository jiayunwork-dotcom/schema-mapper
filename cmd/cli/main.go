package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schema-mapper/schema-mapper/pkg/batch"
	"github.com/schema-mapper/schema-mapper/pkg/compat"
	"github.com/schema-mapper/schema-mapper/pkg/config"
	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/diff"
	"github.com/schema-mapper/schema-mapper/pkg/editor"
	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
	"github.com/schema-mapper/schema-mapper/pkg/migration"
	"github.com/schema-mapper/schema-mapper/pkg/parser"
	"github.com/schema-mapper/schema-mapper/pkg/registry"
	"github.com/schema-mapper/schema-mapper/pkg/report"
)

var (
	outputFormat string
	cfg          *config.Config
)

func main() {
	var err error
	cfg, err = config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to load config: %v\n", err)
	}

	rootCmd := &cobra.Command{
		Use:   "schema-mapper",
		Short: "Schema mapping and format conversion tool for heterogeneous data sources",
		Long: `schema-mapper is a command-line tool for schema mapping and format conversion
between heterogeneous data sources. It supports parsing various schema formats,
automatic mapping generation, data conversion, schema diff, compatibility checking,
batch processing, and report generation.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if f, _ := cmd.Flags().GetString("format"); f != "" {
				outputFormat = f
			} else {
				outputFormat = cfg.DefaultOutputFormat
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&outputFormat, "format", "", "Output format: text (default), json")

	rootCmd.AddCommand(newParseCmd())
	rootCmd.AddCommand(newAutomapCmd())
	rootCmd.AddCommand(newConvertCmd())
	rootCmd.AddCommand(newDiffCmd())
	rootCmd.AddCommand(newCheckCmd())
	rootCmd.AddCommand(newBatchCmd())
	rootCmd.AddCommand(newReportCmd())
	rootCmd.AddCommand(newEditCmd())
	rootCmd.AddCommand(newRegistryCmd())
	rootCmd.AddCommand(newMigrateCmd())
	rootCmd.AddCommand(newVersionCmd())

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newParseCmd() *cobra.Command {
	var format string

	cmd := &cobra.Command{
		Use:   "parse [schema-file]",
		Short: "Parse a schema file and output the internal IR",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := parser.NewParserRegistry()
			schema, err := registry.ParseFile(args[0], format)
			if err != nil {
				return fmt.Errorf("failed to parse schema: %w", err)
			}

			return outputSchema(schema)
		},
	}

	cmd.Flags().StringVar(&format, "schema-format", "", "Schema format: json-schema, avro, protobuf, sql, csv, xsd")
	return cmd
}

func newAutomapCmd() *cobra.Command {
	var sourceFormat, targetFormat, outputFile string

	cmd := &cobra.Command{
		Use:   "automap [source-schema] [target-schema]",
		Short: "Generate automatic mapping suggestions between two schemas",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := parser.NewParserRegistry()

			sourceSchema, err := registry.ParseFile(args[0], sourceFormat)
			if err != nil {
				return fmt.Errorf("failed to parse source schema: %w", err)
			}

			targetSchema, err := registry.ParseFile(args[1], targetFormat)
			if err != nil {
				return fmt.Errorf("failed to parse target schema: %w", err)
			}

			autoMapper := mapper.NewAutoMapper()
			result := autoMapper.GenerateMapping(sourceSchema, targetSchema)

			if outputFile != "" {
				yamlContent := result.ToYAML()
				if err := os.WriteFile(outputFile, []byte(yamlContent), 0644); err != nil {
					return fmt.Errorf("failed to write mapping file: %w", err)
				}
				fmt.Printf("Mapping rules written to: %s\n", outputFile)
			}

			return outputMappingResult(result)
		},
	}

	cmd.Flags().StringVar(&sourceFormat, "source-format", "", "Source schema format")
	cmd.Flags().StringVar(&targetFormat, "target-format", "", "Target schema format")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output YAML mapping file")
	return cmd
}

func newConvertCmd() *cobra.Command {
	var mappingFile, outputFile string
	var inputFormat, outFormat string
	var showProgress bool

	cmd := &cobra.Command{
		Use:   "convert [source-data-file]",
		Short: "Convert data file using mapping rules",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := converter.ConversionOptions{
				ShowProgress: showProgress,
			}
			if inputFormat != "" {
				opts.InputFormat = converter.DataFormat(inputFormat)
			}
			if outFormat != "" {
				opts.OutputFormat = converter.DataFormat(outFormat)
			}

			dc := converter.NewDataConverter(nil, opts)
			result, err := dc.ConvertFile(args[0], mappingFile, outputFile)
			if err != nil {
				return fmt.Errorf("conversion failed: %w", err)
			}

			return outputConversionResult(result)
		},
	}

	cmd.Flags().StringVarP(&mappingFile, "mapping", "m", "", "Mapping rules YAML file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path (required)")
	cmd.Flags().StringVar(&inputFormat, "input-format", "", "Input format: json, csv, xml, ndjson, parquet")
	cmd.Flags().StringVar(&outFormat, "output-format", "", "Output format: json, csv, xml, ndjson")
	cmd.Flags().BoolVarP(&showProgress, "progress", "p", true, "Show progress bar")
	cmd.MarkFlagRequired("mapping")
	cmd.MarkFlagRequired("output")
	return cmd
}

func newDiffCmd() *cobra.Command {
	var format1, format2 string

	cmd := &cobra.Command{
		Use:   "diff [schema-v1] [schema-v2]",
		Short: "Compare two schema versions and show differences",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := parser.NewParserRegistry()

			schema1, err := registry.ParseFile(args[0], format1)
			if err != nil {
				return fmt.Errorf("failed to parse schema v1: %w", err)
			}

			schema2, err := registry.ParseFile(args[1], format2)
			if err != nil {
				return fmt.Errorf("failed to parse schema v2: %w", err)
			}

			result := diff.CompareSchemas(schema1, schema2)
			return outputDiffResult(result)
		},
	}

	cmd.Flags().StringVar(&format1, "v1-format", "", "Schema v1 format")
	cmd.Flags().StringVar(&format2, "v2-format", "", "Schema v2 format")
	return cmd
}

func newCheckCmd() *cobra.Command {
	var sourceFormat, targetFormat string

	cmd := &cobra.Command{
		Use:   "check [source-schema] [target-schema]",
		Short: "Check compatibility between source and target schemas",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			registry := parser.NewParserRegistry()

			sourceSchema, err := registry.ParseFile(args[0], sourceFormat)
			if err != nil {
				return fmt.Errorf("failed to parse source schema: %w", err)
			}

			targetSchema, err := registry.ParseFile(args[1], targetFormat)
			if err != nil {
				return fmt.Errorf("failed to parse target schema: %w", err)
			}

			result := compat.CheckCompatibility(sourceSchema, targetSchema)

			if err := outputCompatibilityResult(result); err != nil {
				return err
			}

			if result.HasIncompatible {
				os.Exit(1)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceFormat, "source-format", "", "Source schema format")
	cmd.Flags().StringVar(&targetFormat, "target-format", "", "Target schema format")
	return cmd
}

func newBatchCmd() *cobra.Command {
	var inputDir, pattern, mappingFile, outputDir string
	var concurrency int
	var resume, showProgress bool

	cmd := &cobra.Command{
		Use:   "batch",
		Short: "Batch convert multiple files using mapping rules",
		RunE: func(cmd *cobra.Command, args []string) error {
			if concurrency <= 0 {
				concurrency = cfg.Concurrency
			}

			opts := batch.BatchOptions{
				InputDir:     inputDir,
				Pattern:      pattern,
				MappingPath:  mappingFile,
				OutputDir:    outputDir,
				Concurrency:  concurrency,
				Resume:       resume,
				ShowProgress: showProgress,
			}

			result, err := batch.BatchConvert(opts)
			if err != nil {
				return fmt.Errorf("batch conversion failed: %w", err)
			}

			return outputBatchResult(result)
		},
	}

	cmd.Flags().StringVar(&inputDir, "input-dir", "", "Input directory (required)")
	cmd.Flags().StringVar(&pattern, "pattern", "*.json", "File pattern to match")
	cmd.Flags().StringVarP(&mappingFile, "mapping", "m", "", "Mapping rules YAML file (required)")
	cmd.Flags().StringVar(&outputDir, "output-dir", "", "Output directory (required)")
	cmd.Flags().IntVarP(&concurrency, "concurrency", "c", 0, "Number of concurrent workers (default: 4)")
	cmd.Flags().BoolVar(&resume, "resume", true, "Resume from checkpoint")
	cmd.Flags().BoolVarP(&showProgress, "progress", "p", true, "Show progress")
	cmd.MarkFlagRequired("input-dir")
	cmd.MarkFlagRequired("mapping")
	cmd.MarkFlagRequired("output-dir")
	return cmd
}

func newReportCmd() *cobra.Command {
	var mappingFile, outputFile string

	cmd := &cobra.Command{
		Use:   "report [mapping-file]",
		Short: "Generate mapping coverage report",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mappingFile = args[0]
			r, err := report.GenerateCoverageReport(mappingFile)
			if err != nil {
				return fmt.Errorf("failed to generate report: %w", err)
			}

			if outputFile != "" {
				if err := r.SaveJSON(outputFile); err != nil {
					return fmt.Errorf("failed to save report: %w", err)
				}
				fmt.Printf("Report saved to: %s\n", outputFile)
			}

			return outputReport(r)
		},
	}

	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output JSON report file")
	return cmd
}

func newEditCmd() *cobra.Command {
	var sourceSchemaPath, targetSchemaPath string

	cmd := &cobra.Command{
		Use:   "edit [mapping-file]",
		Short: "Interactive mapping editor",
		Long: `Open an interactive terminal interface to edit mapping rules.
Use arrow keys or j/k to navigate, and various keys to perform operations on mappings.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ed, err := editor.NewEditor(args[0], sourceSchemaPath, targetSchemaPath)
			if err != nil {
				return fmt.Errorf("failed to initialize editor: %w", err)
			}

			if err := ed.Run(); err != nil {
				return err
			}

			fmt.Println("Editor closed successfully.")
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceSchemaPath, "source", "", "Source schema file path")
	cmd.Flags().StringVar(&targetSchemaPath, "target", "", "Target schema file path")
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("schema-mapper v1.0.0")
			fmt.Println("Built with Go 1.21")
		},
	}
}

func outputSchema(schema *ir.Schema) error {
	if strings.ToLower(outputFormat) == "json" {
		b, err := json.MarshalIndent(schema, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Printf("Schema: %s\n", schema.Name)
	fmt.Printf("Namespace: %s\n", schema.Namespace)
	fmt.Printf("Source Type: %s\n", schema.SourceType)
	fmt.Printf("Version: %s\n", schema.Version)
	fmt.Printf("Description: %s\n\n", schema.Description)
	fmt.Println("Fields:")
	for _, f := range schema.AllFields() {
		if f.IsLeaf() {
			fmt.Printf("  %-40s %-15s nullable=%v\n", f.Path, f.Type, f.Nullable)
			if f.Description != "" {
				fmt.Printf("    %s\n", f.Description)
			}
		}
	}
	return nil
}

func outputMappingResult(result *mapper.AutoMapperResult) error {
	if strings.ToLower(outputFormat) == "json" {
		b, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}

	fmt.Println(result.ToYAML())
	return nil
}

func outputConversionResult(result *converter.ConversionResult) error {
	if strings.ToLower(outputFormat) == "json" {
		fmt.Println(result.ToJSON())
		return nil
	}

	result.PrintSummary()
	return nil
}

func outputDiffResult(result *diff.DiffResult) error {
	if strings.ToLower(outputFormat) == "json" {
		fmt.Println(result.ToJSON())
		return nil
	}

	result.PrintColored()
	return nil
}

func outputCompatibilityResult(result *compat.CompatibilityResult) error {
	if strings.ToLower(outputFormat) == "json" {
		fmt.Println(result.ToJSON())
		return nil
	}

	result.Print()
	return nil
}

func outputBatchResult(result *batch.BatchResult) error {
	if strings.ToLower(outputFormat) == "json" {
		fmt.Println(result.ToJSON())
		return nil
	}

	result.Print()
	return nil
}

func outputReport(r *report.CoverageReport) error {
	if strings.ToLower(outputFormat) == "json" {
		fmt.Println(r.ToJSON())
		return nil
	}

	r.Print()
	return nil
}

func detectFormat(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return "json-schema"
	case ".avsc":
		return "avro"
	case ".proto":
		return "protobuf"
	case ".sql":
		return "sql"
	case ".csv":
		return "csv"
	case ".xsd":
		return "xsd"
	default:
		return ""
	}
}

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Schema version registry management",
		Long:  "Manage schema version registry for tracking schema evolution and generating migration scripts.",
	}

	cmd.AddCommand(newRegistryInitCmd())
	cmd.AddCommand(newRegistryAddCmd())
	cmd.AddCommand(newRegistryListCmd())
	cmd.AddCommand(newRegistryShowCmd())
	cmd.AddCommand(newRegistryDiffCmd())

	return cmd
}

func newRegistryInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new schema registry in current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg := registry.NewRegistry(cwd)
			if err := reg.Init(); err != nil {
				return err
			}

			fmt.Printf("Schema registry initialized at: %s\n", reg.RegistryPath())
			return nil
		},
	}
	return cmd
}

func newRegistryAddCmd() *cobra.Command {
	var schemaName, version string

	cmd := &cobra.Command{
		Use:   "add [schema-file]",
		Short: "Add a schema version to the registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			if err := reg.AddSchemaVersion(schemaName, version, args[0]); err != nil {
				return err
			}

			fmt.Printf("Schema %s version %s added successfully\n", schemaName, version)
			return nil
		},
	}

	cmd.Flags().StringVar(&schemaName, "name", "", "Schema name (required)")
	cmd.Flags().StringVar(&version, "version", "", "Semantic version (major.minor.patch, required)")
	cmd.MarkFlagRequired("name")
	cmd.MarkFlagRequired("version")

	return cmd
}

func newRegistryListCmd() *cobra.Command {
	var schemaName string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered schemas and their versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			if schemaName != "" {
				versions, err := reg.GetSchemaVersions(schemaName)
				if err != nil {
					return err
				}
				sort.Sort(registry.SemVerList(versions))

				return outputSchemaVersions(schemaName, versions)
			}

			allSchemas, err := reg.GetAllSchemas()
			if err != nil {
				return err
			}

			return outputAllSchemas(allSchemas)
		},
	}

	cmd.Flags().StringVar(&schemaName, "name", "", "Filter by schema name")
	return cmd
}

func newRegistryShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [schema-name] [version]",
		Short: "Show a specific schema version",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			schema, err := reg.GetSchema(args[0], args[1])
			if err != nil {
				return err
			}

			return outputSchema(schema)
		},
	}
	return cmd
}

func newRegistryDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff [schema-name] [v1] [v2]",
		Short: "Compare two schema versions and show differences with migration impact",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			schema1, err := reg.GetSchema(args[0], args[1])
			if err != nil {
				return err
			}

			schema2, err := reg.GetSchema(args[0], args[2])
			if err != nil {
				return err
			}

			diffResult := diff.CompareSchemas(schema1, schema2)

			_, impact, err := migration.GenerateMigrationScript(reg, args[0], args[1], args[2])
			if err != nil {
				return err
			}

			if strings.ToLower(outputFormat) == "json" {
				return outputRegistryDiffJSON(diffResult, impact)
			}

			diffResult.PrintColored()
			return outputMigrationImpact(impact)
		},
	}
	return cmd
}

func newMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Schema migration script generation and execution",
		Long:  "Generate, validate, and apply data migration scripts based on schema version differences.",
	}

	cmd.AddCommand(newMigrateGenerateCmd())
	cmd.AddCommand(newMigrateValidateCmd())
	cmd.AddCommand(newMigrateApplyCmd())
	cmd.AddCommand(newMigratePlanCmd())
	cmd.AddCommand(newMigrateDryRunCmd())
	cmd.AddCommand(newMigrateExecuteCmd())
	cmd.AddCommand(newMigrateRollbackCmd())

	return cmd
}

func newMigrateGenerateCmd() *cobra.Command {
	var fromV, toV, outputDir string

	cmd := &cobra.Command{
		Use:   "generate [schema-name]",
		Short: "Generate migration script between two schema versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			script, impact, err := migration.GenerateMigrationScript(reg, args[0], fromV, toV)
			if err != nil {
				return err
			}

			if outputDir != "" {
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("failed to create output directory: %w", err)
				}

				fileName := fmt.Sprintf("migrate_%s_to_%s.yaml", fromV, toV)
				filePath := filepath.Join(outputDir, fileName)
				if err := script.Save(filePath); err != nil {
					return err
				}
				fmt.Printf("Migration script saved to: %s\n", filePath)
			}

			if strings.ToLower(outputFormat) == "json" {
				jsonStr, err := script.ToJSON()
				if err != nil {
					return err
				}
				fmt.Println(jsonStr)
				return nil
			}

			return outputMigrationScript(script, impact)
		},
	}

	cmd.Flags().StringVar(&fromV, "from", "", "Source version (required)")
	cmd.Flags().StringVar(&toV, "to", "", "Target version (required)")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for migration script")
	cmd.MarkFlagRequired("from")
	cmd.MarkFlagRequired("to")

	return cmd
}

func newMigrateValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [migration-script.yaml]",
		Short: "Validate a migration script for correctness",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			script, err := migration.LoadMigrationScript(args[0])
			if err != nil {
				return err
			}

			errors := migration.ValidateMigrationScript(script, reg)

			if strings.ToLower(outputFormat) == "json" {
				return outputValidationErrorsJSON(errors)
			}

			return outputValidationErrors(errors)
		},
	}
	return cmd
}

func newMigrateApplyCmd() *cobra.Command {
	var sourceFile, outputFile, inputFormat, outFormat string
	var showProgress bool

	cmd := &cobra.Command{
		Use:   "apply [migration-script.yaml]",
		Short: "Apply a migration script to data files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := converter.ConversionOptions{
				ShowProgress: showProgress,
			}
			if inputFormat != "" {
				opts.InputFormat = converter.DataFormat(inputFormat)
			}
			if outFormat != "" {
				opts.OutputFormat = converter.DataFormat(outFormat)
			}

			result, err := migration.ApplyMigrationScript(args[0], sourceFile, outputFile, opts)
			if err != nil {
				return err
			}

			if strings.ToLower(outputFormat) == "json" {
				fmt.Println(result.ToJSON())
				return nil
			}

			result.Print()
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceFile, "source", "", "Source data file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path (required)")
	cmd.Flags().StringVar(&inputFormat, "input-format", "", "Input format: json, csv, ndjson")
	cmd.Flags().StringVar(&outFormat, "output-format", "", "Output format: json, csv, ndjson")
	cmd.Flags().BoolVarP(&showProgress, "progress", "p", true, "Show progress bar")
	cmd.MarkFlagRequired("source")
	cmd.MarkFlagRequired("output")

	return cmd
}

func outputSchemaVersions(schemaName string, versions []*registry.SemVer) error {
	fmt.Printf("Schema: %s\n", schemaName)
	fmt.Println("Versions (latest first):")
	for i, v := range versions {
		fmt.Printf("  %d. %s\n", i+1, v.String())
	}
	if len(versions) == 0 {
		fmt.Println("  (no versions registered)")
	}
	return nil
}

func outputAllSchemas(schemas map[string][]*registry.SemVer) error {
	if len(schemas) == 0 {
		fmt.Println("No schemas registered in registry.")
		return nil
	}

	names := make([]string, 0, len(schemas))
	for name := range schemas {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		versions := schemas[name]
		fmt.Printf("\nSchema: %s\n", name)
		fmt.Printf("  Version count: %d\n", len(versions))
		if len(versions) > 0 {
			versionStrs := make([]string, 0, len(versions))
			for _, v := range versions {
				versionStrs = append(versionStrs, v.String())
			}
			fmt.Printf("  Versions: %s\n", strings.Join(versionStrs, ", "))
		}
	}
	return nil
}

func outputMigrationImpact(impact *migration.MigrationImpact) error {
	fmt.Println("\n\033[1m=== Migration Impact Assessment ===\033[0m")
	fmt.Printf("Affected fields:    %d\n", impact.AffectedFields)
	fmt.Printf("Has breaking change: %v\n", impact.HasBreakingChange)
	if len(impact.BreakingChanges) > 0 {
		fmt.Println("Breaking changes:")
		for _, bc := range impact.BreakingChanges {
			fmt.Printf("  - %s\n", bc)
		}
	}

	riskColor := "\033[32m"
	switch impact.RiskLevel {
	case migration.RiskMedium:
		riskColor = "\033[33m"
	case migration.RiskHigh:
		riskColor = "\033[31m"
	}

	fmt.Printf("Risk level:         %s%s\033[0m\n", riskColor, impact.RiskLevel)
	fmt.Printf("Migration strategy: %s\n", impact.MigrationStrategy)

	return nil
}

func outputRegistryDiffJSON(dr *diff.DiffResult, impact *migration.MigrationImpact) error {
	type output struct {
		Diff   *diff.DiffResult           `json:"diff"`
		Impact *migration.MigrationImpact `json:"migrationImpact"`
	}
	b, err := json.MarshalIndent(output{Diff: dr, Impact: impact}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func outputMigrationScript(script *migration.MigrationScript, impact *migration.MigrationImpact) error {
	fmt.Printf("=== Migration Script: %s %s -> %s\n", script.SchemaName, script.FromVersion, script.ToVersion)
	fmt.Printf("Created at: %s\n\n", script.CreatedAt)
	fmt.Printf("Operations (%d):\n", len(script.Operations))

	for i, op := range script.Operations {
		riskColor := "\033[32m"
		switch op.RiskLevel {
		case migration.RiskMedium:
			riskColor = "\033[33m"
		case migration.RiskHigh:
			riskColor = "\033[31m"
		}

		fmt.Printf("\n  \033[1m[%d] %s%s\033[0m\n", i+1, riskColor, op.Op)
		fmt.Printf("      Field: %s\n", op.FieldPath)
		if op.NewFieldPath != "" {
			fmt.Printf("      New field: %s\n", op.NewFieldPath)
		}
		if op.NewType != "" {
			fmt.Printf("      Type: %s -> %s\n", op.OldType, op.NewType)
		}
		if op.DefaultValue != nil {
			fmt.Printf("      Default value: %v\n", op.DefaultValue)
		}
		if op.FallbackValue != nil {
			fmt.Printf("      Fallback value: %v\n", op.FallbackValue)
		}
		fmt.Printf("      Risk: %s%s\033[0m\n", riskColor, op.RiskLevel)
		fmt.Printf("      %s\n", op.Description)
	}

	fmt.Println()
	outputMigrationImpact(impact)

	return nil
}

func outputValidationErrors(errors []migration.ValidationError) error {
	if len(errors) == 0 {
		fmt.Println("\033[32m✓ Migration script is valid!\033[0m")
		return nil
	}

	fmt.Printf("\033[31m✗ Found %d validation error(s):\033[0m\n", len(errors))
	for _, e := range errors {
		fmt.Printf("  %s\n", e.String())
	}
	os.Exit(1)
	return nil
}

func outputValidationErrorsJSON(errors []migration.ValidationError) error {
	type output struct {
		Valid  bool                        `json:"valid"`
		Errors []migration.ValidationError `json:"errors,omitempty"`
		Count  int                         `json:"errorCount"`
	}

	out := output{
		Valid:  len(errors) == 0,
		Errors: errors,
		Count:  len(errors),
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))

	if len(errors) > 0 {
		os.Exit(1)
	}
	return nil
}

func newMigratePlanCmd() *cobra.Command {
	var fromV, toV, dataSample, outputDir string

	cmd := &cobra.Command{
		Use:   "plan [schema-name]",
		Short: "Generate a migration plan with preview and risk assessment",
		Long: `Generate a migration plan file (plan.yaml) that includes:
- Source and target version information
- Total number of operation steps
- Risk statistics grouped by risk level (low/medium/high)
- Estimated affected records (if --data-sample is provided)
- Data quality warnings from sample analysis`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}

			reg, err := registry.FindRegistry(cwd)
			if err != nil {
				return err
			}

			plan, err := migration.GenerateMigrationPlan(reg, args[0], fromV, toV, dataSample)
			if err != nil {
				return err
			}

			if outputDir != "" {
				if err := os.MkdirAll(outputDir, 0755); err != nil {
					return fmt.Errorf("failed to create output directory: %w", err)
				}
				filePath := filepath.Join(outputDir, "plan.yaml")
				if err := plan.Save(filePath); err != nil {
					return err
				}
				fmt.Printf("Migration plan saved to: %s\n", filePath)
			}

			if strings.ToLower(outputFormat) == "json" {
				jsonStr, err := plan.ToJSON()
				if err != nil {
					return err
				}
				fmt.Println(jsonStr)
				return nil
			}

			plan.Print()
			return nil
		},
	}

	cmd.Flags().StringVar(&fromV, "from", "", "Source version (required)")
	cmd.Flags().StringVar(&toV, "to", "", "Target version (required)")
	cmd.Flags().StringVar(&dataSample, "data-sample", "", "Path to data sample file for quality analysis")
	cmd.Flags().StringVarP(&outputDir, "output", "o", "", "Output directory for plan.yaml")
	cmd.MarkFlagRequired("from")
	cmd.MarkFlagRequired("to")

	return cmd
}

func newMigrateDryRunCmd() *cobra.Command {
	var sourceFile, outputFile string

	cmd := &cobra.Command{
		Use:   "dry-run [plan.yaml]",
		Short: "Simulate migration execution and generate detailed report",
		Long: `Simulate executing a migration plan without writing output files.
Generates a detailed JSON report including:
- Success and failure counts per operation
- Skipped fields statistics grouped by path
- Missing fallback value failures
- Top 3 failure reasons per operation
- Recommendations for high-risk operations with >5% failure rate`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			report, err := migration.DryRunMigration(args[0], sourceFile)
			if err != nil {
				return err
			}

			if outputFile != "" {
				if err := report.Save(outputFile); err != nil {
					return fmt.Errorf("failed to save dry-run report: %w", err)
				}
				fmt.Printf("Dry-run report saved to: %s\n", outputFile)
			}

			if strings.ToLower(outputFormat) == "json" {
				jsonStr, err := report.ToJSON()
				if err != nil {
					return err
				}
				fmt.Println(jsonStr)
				return nil
			}

			report.Print()
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceFile, "source", "", "Source data file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output JSON report file")
	cmd.MarkFlagRequired("source")

	return cmd
}

func newMigrateExecuteCmd() *cobra.Command {
	var sourceFile, outputFile, inputFormat, outFormat string
	var step int
	var showProgress bool

	cmd := &cobra.Command{
		Use:   "execute [plan.yaml]",
		Short: "Execute migration plan with step control and checkpoint support",
		Long: `Execute a migration plan with support for:
- Partial execution: only execute first N steps with --step N
- Checkpoint resume: if plan has partial state, continue from last executed step
- Automatic rollback script generation for each execution batch`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := converter.ConversionOptions{
				ShowProgress: showProgress,
			}
			if inputFormat != "" {
				opts.InputFormat = converter.DataFormat(inputFormat)
			}
			if outFormat != "" {
				opts.OutputFormat = converter.DataFormat(outFormat)
			}

			result, err := migration.ExecuteMigrationPlan(args[0], sourceFile, outputFile, step, opts)
			if err != nil {
				return err
			}

			if strings.ToLower(outputFormat) == "json" {
				fmt.Println(result.ToJSON())
				return nil
			}

			result.Print()
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceFile, "source", "", "Source data file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path (required)")
	cmd.Flags().IntVar(&step, "step", 0, "Execute only first N steps (0 = all steps)")
	cmd.Flags().StringVar(&inputFormat, "input-format", "", "Input format: json, csv, ndjson")
	cmd.Flags().StringVar(&outFormat, "output-format", "", "Output format: json, csv, ndjson")
	cmd.Flags().BoolVarP(&showProgress, "progress", "p", true, "Show progress bar")
	cmd.MarkFlagRequired("source")
	cmd.MarkFlagRequired("output")

	return cmd
}

func newMigrateRollbackCmd() *cobra.Command {
	var sourceFile, outputFile, inputFormat, outFormat string
	var showProgress bool

	cmd := &cobra.Command{
		Use:   "rollback [rollback-script.yaml]",
		Short: "Execute a rollback script to undo previous migration",
		Long: `Execute a rollback script generated by a previous migrate execute command.
The rollback script contains inverse operations to restore the data to its previous state.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := converter.ConversionOptions{
				ShowProgress: showProgress,
			}
			if inputFormat != "" {
				opts.InputFormat = converter.DataFormat(inputFormat)
			}
			if outFormat != "" {
				opts.OutputFormat = converter.DataFormat(outFormat)
			}

			result, err := migration.RollbackMigration(args[0], sourceFile, outputFile, opts)
			if err != nil {
				return err
			}

			if strings.ToLower(outputFormat) == "json" {
				fmt.Println(result.ToJSON())
				return nil
			}

			fmt.Println("\n=== Rollback Result ===")
			result.Print()
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceFile, "source", "", "Source data file (required)")
	cmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path (required)")
	cmd.Flags().StringVar(&inputFormat, "input-format", "", "Input format: json, csv, ndjson")
	cmd.Flags().StringVar(&outFormat, "output-format", "", "Output format: json, csv, ndjson")
	cmd.Flags().BoolVarP(&showProgress, "progress", "p", true, "Show progress bar")
	cmd.MarkFlagRequired("source")
	cmd.MarkFlagRequired("output")

	return cmd
}
