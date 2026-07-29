package providerprobe

import (
	"context"
	"fmt"
)

// InspectRecipeMode parses and validates one local recipe without running its
// qualified source-analysis pipeline.
func InspectRecipeMode(path string) (Mode, error) {
	recipe, err := loadRecipe(path)
	if err != nil {
		return "", err
	}
	return recipe.mode, nil
}

// Run loads one local recipe and returns only detached in-memory artifacts.
// It never publishes artifacts.
func Run(ctx context.Context, options RunOptions) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("provider probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("provider probe cancelled: %w", err)
	}
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
	return runQualified(ctx, recipe)
}
