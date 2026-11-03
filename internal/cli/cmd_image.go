package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/konglong87/super-niuma/internal/config"
	"github.com/konglong87/super-niuma/internal/imagegen"
)

type imageCommandRequest struct {
	operation    string
	prompt       string
	image        string
	out          string
	model        string
	quality      string
	size         string
	outputFormat string
	background   string
	force        bool
}

type imageCommandResult struct {
	Operation   string `json:"operation"`
	Model       string `json:"model"`
	MediaType   string `json:"media_type"`
	Path        string `json:"path"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	GeneratedAt string `json:"generated_at"`
}

func imageCommand(args []string, cwd string, stdout io.Writer) error {
	req, err := parseImageCommand(args)
	if err != nil {
		return err
	}
	resolved, err := config.ResolveImageGeneration(cwd)
	if err != nil {
		return err
	}
	if !resolved.Enabled {
		return imagegen.ErrImageGenerationDisabled
	}
	model := firstNonEmptyString(req.model, resolved.Model)
	quality := firstNonEmptyString(req.quality, resolved.Quality)
	size := firstNonEmptyString(req.size, resolved.Size)
	format := firstNonEmptyString(req.outputFormat, resolved.OutputFormat)
	background := firstNonEmptyString(req.background, resolved.Background)
	provider := imagegen.NewOpenAICompatibleImagesProvider(imagegen.ProviderConfig{
		BaseURL: resolved.BaseURL, APIKey: resolved.APIKey, AuthToken: resolved.AuthToken,
		Timeout: time.Duration(resolved.TimeoutSeconds) * time.Second, MaxRetries: 1,
	})
	var output imagegen.ProviderImage
	switch req.operation {
	case imagegen.OperationGenerate:
		output, err = provider.Generate(context.Background(), imagegen.ProviderGenerateRequest{Prompt: req.prompt, Model: model, Quality: quality, Size: size, OutputFormat: format, Background: background})
	case imagegen.OperationEdit:
		file, openErr := os.Open(resolveInputPath(cwd, req.image))
		if openErr != nil {
			return fmt.Errorf("--image: %w", openErr)
		}
		defer file.Close()
		output, err = provider.Edit(context.Background(), imagegen.ProviderEditRequest{Prompt: req.prompt, Model: model, Quality: quality, Size: size, OutputFormat: format, Background: background, Image: file, ImageName: filepath.Base(req.image), ImageType: mediaTypeForImagePath(req.image)})
	default:
		return fmt.Errorf("unsupported image operation %q", req.operation)
	}
	if err != nil {
		return err
	}
	if len(output.Data) == 0 {
		return errors.New("image provider returned no inline image data")
	}
	path, err := imageOutputPath(cwd, req, format)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if !req.force {
		if _, statErr := os.Stat(path); statErr == nil {
			return fmt.Errorf("output already exists: %s (use --force to replace)", path)
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
	}
	if err := os.WriteFile(path, output.Data, 0o600); err != nil {
		return err
	}
	sum := sha256.Sum256(output.Data)
	result := imageCommandResult{Operation: req.operation, Model: model, MediaType: firstNonEmptyString(output.MediaType, "image/"+format), Path: path, SizeBytes: int64(len(output.Data)), SHA256: hex.EncodeToString(sum[:]), GeneratedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	return writePrettyJSON(stdout, result)
}

func parseImageCommand(args []string) (imageCommandRequest, error) {
	if len(args) == 0 || (args[0] != imagegen.OperationGenerate && args[0] != imagegen.OperationEdit) {
		return imageCommandRequest{}, errors.New("image requires generate or edit")
	}
	req := imageCommandRequest{operation: args[0]}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--prompt":
			value, err := flagValue(args, &i, "--prompt")
			if err != nil {
				return req, err
			}
			req.prompt = value
		case "--image":
			value, err := flagValue(args, &i, "--image")
			if err != nil {
				return req, err
			}
			req.image = value
		case "--out":
			value, err := flagValue(args, &i, "--out")
			if err != nil {
				return req, err
			}
			req.out = value
		case "--model":
			value, err := flagValue(args, &i, "--model")
			if err != nil {
				return req, err
			}
			req.model = value
		case "--quality":
			value, err := flagValue(args, &i, "--quality")
			if err != nil {
				return req, err
			}
			req.quality = value
		case "--size":
			value, err := flagValue(args, &i, "--size")
			if err != nil {
				return req, err
			}
			req.size = value
		case "--output-format":
			value, err := flagValue(args, &i, "--output-format")
			if err != nil {
				return req, err
			}
			req.outputFormat = value
		case "--background":
			value, err := flagValue(args, &i, "--background")
			if err != nil {
				return req, err
			}
			req.background = value
		case "--force":
			req.force = true
		default:
			return req, fmt.Errorf("unknown image option: %s", args[i])
		}
	}
	if strings.TrimSpace(req.prompt) == "" {
		return req, errors.New("--prompt is required")
	}
	if req.operation == imagegen.OperationEdit && strings.TrimSpace(req.image) == "" {
		return req, errors.New("--image is required for edit")
	}
	return req, nil
}

func imageOutputPath(cwd string, req imageCommandRequest, format string) (string, error) {
	if strings.TrimSpace(req.out) != "" {
		return resolveInputPath(cwd, req.out), nil
	}
	ext := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(format)), ".")
	if ext == "" {
		ext = "png"
	}
	return filepath.Join(cwd, "output", "imagegen", fmt.Sprintf("image-%d.%s", time.Now().UnixNano(), ext)), nil
}

func mediaTypeForImagePath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return "image/png"
	}
}
