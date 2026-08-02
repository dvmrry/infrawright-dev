package main

import (
	"context"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
)

func (captureProvider) ValidateResourceTypeConfig(context.Context, *tfprotov5.ValidateResourceTypeConfigRequest) (*tfprotov5.ValidateResourceTypeConfigResponse, error) {
	return &tfprotov5.ValidateResourceTypeConfigResponse{}, nil
}

func (captureProvider) UpgradeResourceState(context.Context, *tfprotov5.UpgradeResourceStateRequest) (*tfprotov5.UpgradeResourceStateResponse, error) {
	return &tfprotov5.UpgradeResourceStateResponse{}, nil
}

func (captureProvider) ReadResource(context.Context, *tfprotov5.ReadResourceRequest) (*tfprotov5.ReadResourceResponse, error) {
	return &tfprotov5.ReadResourceResponse{}, nil
}

func (captureProvider) PlanResourceChange(context.Context, *tfprotov5.PlanResourceChangeRequest) (*tfprotov5.PlanResourceChangeResponse, error) {
	return &tfprotov5.PlanResourceChangeResponse{}, nil
}

func (captureProvider) ApplyResourceChange(context.Context, *tfprotov5.ApplyResourceChangeRequest) (*tfprotov5.ApplyResourceChangeResponse, error) {
	return &tfprotov5.ApplyResourceChangeResponse{}, nil
}

func (captureProvider) ImportResourceState(context.Context, *tfprotov5.ImportResourceStateRequest) (*tfprotov5.ImportResourceStateResponse, error) {
	return &tfprotov5.ImportResourceStateResponse{}, nil
}

func (captureProvider) MoveResourceState(context.Context, *tfprotov5.MoveResourceStateRequest) (*tfprotov5.MoveResourceStateResponse, error) {
	return &tfprotov5.MoveResourceStateResponse{}, nil
}

func (captureProvider) UpgradeResourceIdentity(context.Context, *tfprotov5.UpgradeResourceIdentityRequest) (*tfprotov5.UpgradeResourceIdentityResponse, error) {
	return &tfprotov5.UpgradeResourceIdentityResponse{}, nil
}

func (captureProvider) GenerateResourceConfig(context.Context, *tfprotov5.GenerateResourceConfigRequest) (*tfprotov5.GenerateResourceConfigResponse, error) {
	return &tfprotov5.GenerateResourceConfigResponse{}, nil
}

func (captureProvider) GetFunctions(context.Context, *tfprotov5.GetFunctionsRequest) (*tfprotov5.GetFunctionsResponse, error) {
	return &tfprotov5.GetFunctionsResponse{Functions: map[string]*tfprotov5.Function{}}, nil
}

func (captureProvider) CallFunction(context.Context, *tfprotov5.CallFunctionRequest) (*tfprotov5.CallFunctionResponse, error) {
	return &tfprotov5.CallFunctionResponse{}, nil
}

func (captureProvider) ValidateEphemeralResourceConfig(context.Context, *tfprotov5.ValidateEphemeralResourceConfigRequest) (*tfprotov5.ValidateEphemeralResourceConfigResponse, error) {
	return &tfprotov5.ValidateEphemeralResourceConfigResponse{}, nil
}

func (captureProvider) OpenEphemeralResource(context.Context, *tfprotov5.OpenEphemeralResourceRequest) (*tfprotov5.OpenEphemeralResourceResponse, error) {
	return &tfprotov5.OpenEphemeralResourceResponse{}, nil
}

func (captureProvider) RenewEphemeralResource(context.Context, *tfprotov5.RenewEphemeralResourceRequest) (*tfprotov5.RenewEphemeralResourceResponse, error) {
	return &tfprotov5.RenewEphemeralResourceResponse{}, nil
}

func (captureProvider) CloseEphemeralResource(context.Context, *tfprotov5.CloseEphemeralResourceRequest) (*tfprotov5.CloseEphemeralResourceResponse, error) {
	return &tfprotov5.CloseEphemeralResourceResponse{}, nil
}
