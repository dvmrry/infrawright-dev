package providerprobe

import (
	"context"
	"fmt"
)

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
