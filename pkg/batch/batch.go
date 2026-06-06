package batch

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/schema-mapper/schema-mapper/pkg/converter"
)

type BatchOptions struct {
	InputDir    string
	Pattern     string
	MappingPath string
	OutputDir   string
	Concurrency int
	Resume      bool
	ShowProgress bool
}

type BatchResult struct {
	TotalFiles    int
	SuccessFiles  int
	FailedFiles   int
	SkippedFiles  int
	TotalDuration time.Duration
	FailedList    []string
	SkippedList   []string
	SuccessList   []string
}

type FileResult struct {
	Input     string
	Output    string
	Success   bool
	Error     string
	Duration  time.Duration
	Records   int64
}

type checkpoint struct {
	CompletedFiles map[string]time.Time `json:"completedFiles"`
}

const checkpointFile = ".checkpoint"

func BatchConvert(opts BatchOptions) (*BatchResult, error) {
	start := time.Now()

	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
	}

	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	files, err := findMatchingFiles(opts.InputDir, opts.Pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to find files: %w", err)
	}

	if len(files) == 0 {
		return &BatchResult{}, fmt.Errorf("no files matched pattern: %s", opts.Pattern)
	}

	chk, err := loadCheckpoint(opts.OutputDir)
	if err != nil {
		chk = &checkpoint{CompletedFiles: make(map[string]time.Time)}
	}

	result := &BatchResult{
		TotalFiles:  len(files),
		FailedList:  make([]string, 0),
		SkippedList: make([]string, 0),
		SuccessList: make([]string, 0),
	}

	sem := make(chan struct{}, opts.Concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	convertOpts := converter.ConversionOptions{
		ShowProgress: false,
	}

	for _, inputPath := range files {
		if opts.Resume {
			if _, exists := chk.CompletedFiles[inputPath]; exists {
				mu.Lock()
				result.SkippedFiles++
				result.SkippedList = append(result.SkippedList, inputPath)
				mu.Unlock()
				continue
			}
		}

		relPath, err := filepath.Rel(opts.InputDir, inputPath)
		if err != nil {
			relPath = filepath.Base(inputPath)
		}
		outputPath := filepath.Join(opts.OutputDir, relPath)
		ext := filepath.Ext(outputPath)
		outputPath = outputPath[:len(outputPath)-len(ext)] + ".json"

		wg.Add(1)
		sem <- struct{}{}

		go func(input, output string) {
			defer wg.Done()
			defer func() { <-sem }()

			fileStart := time.Now()

			if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
				mu.Lock()
				result.FailedFiles++
				result.FailedList = append(result.FailedList, fmt.Sprintf("%s: %v", input, err))
				mu.Unlock()
				return
			}

			dc := converter.NewDataConverter(nil, convertOpts)
			convResult, err := dc.ConvertFile(input, opts.MappingPath, output)

			mu.Lock()
			if err != nil {
				result.FailedFiles++
				result.FailedList = append(result.FailedList, fmt.Sprintf("%s: %v", input, err))
			} else {
				result.SuccessFiles++
				result.SuccessList = append(result.SuccessList, input)
				chk.CompletedFiles[input] = time.Now()
				saveCheckpoint(opts.OutputDir, chk)
			}
			mu.Unlock()

			_ = fileStart
			_ = convResult
		}(inputPath, outputPath)
	}

	wg.Wait()
	close(sem)

	result.TotalDuration = time.Since(start)
	saveCheckpoint(opts.OutputDir, chk)

	return result, nil
}

func findMatchingFiles(root, pattern string) ([]string, error) {
	var files []string

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		matched, err := filepath.Match(pattern, filepath.Base(path))
		if err != nil {
			return err
		}
		if matched {
			files = append(files, path)
		}
		return nil
	})

	return files, err
}

func loadCheckpoint(dir string) (*checkpoint, error) {
	path := filepath.Join(dir, checkpointFile)
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var chk checkpoint
	if err := json.Unmarshal(data, &chk); err != nil {
		return nil, err
	}

	if chk.CompletedFiles == nil {
		chk.CompletedFiles = make(map[string]time.Time)
	}

	return &chk, nil
}

func saveCheckpoint(dir string, chk *checkpoint) error {
	path := filepath.Join(dir, checkpointFile)
	data, err := json.MarshalIndent(chk, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

func (r *BatchResult) Print() {
	fmt.Printf("\n=== Batch Conversion Summary\n")
	fmt.Printf("Total files:   %d\n", r.TotalFiles)
	fmt.Printf("Success:       %d\033[0m\n", r.SuccessFiles)
	if r.FailedFiles > 0 {
		fmt.Printf("\033[31mFailed:        %d\033[0m\n", r.FailedFiles)
	} else {
		fmt.Printf("Failed:        %d\n", r.FailedFiles)
	}
	fmt.Printf("Skipped:       %d\n", r.SkippedFiles)
	fmt.Printf("Duration:      %v\n", r.TotalDuration)

	if len(r.SkippedList) > 0 {
		fmt.Printf("\nSkipped files (already completed):\n")
		for _, f := range r.SkippedList[:min(len(r.SkippedList), 5)] {
			fmt.Printf("  %s\n", f)
		}
		if len(r.SkippedList) > 5 {
			fmt.Printf("  ... and %d more\n", len(r.SkippedList)-5)
		}
	}

	if len(r.FailedList) > 0 {
		fmt.Printf("\n\033[31mFailed files:\033[0m\n")
		for _, f := range r.FailedList[:min(len(r.FailedList), 10)] {
			fmt.Printf("  %s\n", f)
		}
		if len(r.FailedList) > 10 {
			fmt.Printf("  ... and %d more\n", len(r.FailedList)-10)
		}
	}

	if r.FailedFiles > 0 {
		fmt.Printf("\n\033[31m✗ Some files failed to convert\033[0m\n")
	} else {
		fmt.Printf("\n\033[32m✓ All files converted successfully\033[0m\n")
	}
}

func (r *BatchResult) ToJSON() string {
	b, _ := json.MarshalIndent(r, "", "  ")
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
