package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/schema-mapper/schema-mapper/pkg/batch"
	"github.com/schema-mapper/schema-mapper/pkg/compat"
	"github.com/schema-mapper/schema-mapper/pkg/config"
	"github.com/schema-mapper/schema-mapper/pkg/converter"
	"github.com/schema-mapper/schema-mapper/pkg/diff"
	"github.com/schema-mapper/schema-mapper/pkg/ir"
	"github.com/schema-mapper/schema-mapper/pkg/mapper"
	"github.com/schema-mapper/schema-mapper/pkg/parser"
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
