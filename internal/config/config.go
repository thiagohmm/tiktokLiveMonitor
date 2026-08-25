// Package config holds application configuration, model definitions, and settings.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ModelInfo describes a downloadable LLM model.
type ModelInfo struct {
	Name     string `json:"name"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
}

// ModelConfig persists the selected model key.
type ModelConfig struct {
	SelectedModel string `json:"selectedModel"`
}

// Settings holds user-adjustable runtime settings.
type Settings struct {
	ModerationEnabled   bool   `json:"moderationEnabled"`
	AIModerationEnabled bool   `json:"aiModerationEnabled"`
	LogLevel            string `json:"logLevel"`
}

// Default settings.
var DefaultSettings = Settings{
	ModerationEnabled:   true,
	AIModerationEnabled: true,
	LogLevel:            "info",
}

// Models is the registry of available LLM models.
var Models = map[string]ModelInfo{
	"gemma-4b": {
		Name:     "Gemma 4B (Padrão)",
		Filename: "gemma-4-E4B-it-Q4_K_M.gguf",
		URL:      "https://huggingface.co/unsloth/gemma-4-E4B-it-GGUF/resolve/main/gemma-4-E4B-it-Q4_K_M.gguf",
	},
	"llama-3.2-3b": {
		Name:     "Llama 3.2 (3B Instruct)",
		Filename: "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
		URL:      "https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct-Q4_K_M.gguf",
	},
}

const configFileName = "model-config.json"
const defaultModelKey = "gemma-4b"

var (
	configMu    sync.RWMutex
	modelConfig ModelConfig
	configPath  string
)

// InitConfig loads or creates the model configuration file.
func InitConfig(baseDir string) error {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	configPath = filepath.Join(baseDir, configFileName)
	modelConfig = ModelConfig{SelectedModel: defaultModelKey}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return saveConfig()
		}
		return fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, &modelConfig); err != nil {
		modelConfig.SelectedModel = defaultModelKey
		return saveConfig()
	}

	if modelConfig.SelectedModel == "" {
		modelConfig.SelectedModel = defaultModelKey
	}

	return nil
}

func saveConfig() error {
	data, err := json.MarshalIndent(modelConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(configPath, data, 0644)
}

// GetSelectedModel returns the current model key.
func GetSelectedModel() string {
	configMu.RLock()
	defer configMu.RUnlock()
	return modelConfig.SelectedModel
}

// SetSelectedModel changes the model key. Returns false if key is invalid.
func SetSelectedModel(key string) bool {
	if _, ok := Models[key]; !ok {
		return false
	}
	configMu.Lock()
	defer configMu.Unlock()
	modelConfig.SelectedModel = key
	if err := saveConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving model config: %v\n", err)
	}
	return true
}

// GetModelInfo returns info for the currently selected model.
func GetModelInfo() ModelInfo {
	key := GetSelectedModel()
	info, ok := Models[key]
	if !ok {
		info = Models[defaultModelKey]
	}
	return info
}

// GGUFPath returns the filesystem path to the model GGUF file.
func GGUFPath(modelsDir string) string {
	info := GetModelInfo()
	return filepath.Join(modelsDir, info.Filename)
}

// DownloadURL returns the URL to download the model GGUF.
func DownloadURL() string {
	return GetModelInfo().URL
}
