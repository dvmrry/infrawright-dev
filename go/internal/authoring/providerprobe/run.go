package providerprobe

import (
	"context"
	"fmt"
)

// InspectRecipeMode parses and validates one local recipe without running its
// legacy preparation or qualified source-analysis pipeline.
func InspectRecipeMode(path string) (Mode, error) {
	recipe, err := loadRecipe(path)
	if err != nil {
		return "", err
	}
	return recipe.mode, nil
}

// Run loads one local recipe, chooses its categorical contract, and returns
// only detached in-memory artifacts. It neither publishes artifacts nor lets a
// qualified recipe reach a legacy preparation capability.
func Run(ctx context.Context, options RunOptions) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("provider probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("provider probe cancelled: %w", err)
	}
	options.Environment = cloneStringMap(options.Environment)
	recipe, err := loadRecipe(options.RecipePath)
	if err != nil {
		return Result{}, err
	}
	if options.ExpectedMode != "" && recipe.mode != options.ExpectedMode {
		return Result{}, fmt.Errorf("provider probe recipe mode changed after preflight: got %q, want %q", recipe.mode, options.ExpectedMode)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("provider probe cancelled: %w", err)
	}
	switch recipe.mode {
	case QualifiedV2:
		return runQualified(ctx, recipe)
	case LegacyV1:
		if options.LegacyHost == nil {
			options.LegacyHost = newDefaultLegacyHost(options.Environment)
		}
		return runLegacy(ctx, recipe, options)
	default:
		return Result{}, fmt.Errorf("unsupported provider probe mode %q", recipe.mode)
	}
}
