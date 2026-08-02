package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-go/tfprotov5"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const dataSourceType = "capture_item"

type captureProvider struct{}

func (captureProvider) GetMetadata(context.Context, *tfprotov5.GetMetadataRequest) (*tfprotov5.GetMetadataResponse, error) {
	return &tfprotov5.GetMetadataResponse{
		DataSources: []tfprotov5.DataSourceMetadata{{TypeName: dataSourceType}},
	}, nil
}

func (captureProvider) GetResourceIdentitySchemas(context.Context, *tfprotov5.GetResourceIdentitySchemasRequest) (*tfprotov5.GetResourceIdentitySchemasResponse, error) {
	return &tfprotov5.GetResourceIdentitySchemasResponse{}, nil
}

func (captureProvider) GetProviderSchema(context.Context, *tfprotov5.GetProviderSchemaRequest) (*tfprotov5.GetProviderSchemaResponse, error) {
	return &tfprotov5.GetProviderSchemaResponse{
		Provider: &tfprotov5.Schema{Block: &tfprotov5.SchemaBlock{}},
		DataSourceSchemas: map[string]*tfprotov5.Schema{
			dataSourceType: captureItemSchema(),
		},
	}, nil
}

func (captureProvider) PrepareProviderConfig(context.Context, *tfprotov5.PrepareProviderConfigRequest) (*tfprotov5.PrepareProviderConfigResponse, error) {
	return &tfprotov5.PrepareProviderConfigResponse{}, nil
}

func (captureProvider) ConfigureProvider(context.Context, *tfprotov5.ConfigureProviderRequest) (*tfprotov5.ConfigureProviderResponse, error) {
	return &tfprotov5.ConfigureProviderResponse{}, nil
}

func (captureProvider) StopProvider(context.Context, *tfprotov5.StopProviderRequest) (*tfprotov5.StopProviderResponse, error) {
	return &tfprotov5.StopProviderResponse{}, nil
}

func captureItemSchema() *tfprotov5.Schema {
	return &tfprotov5.Schema{Block: &tfprotov5.SchemaBlock{
		Attributes: []*tfprotov5.SchemaAttribute{
			{Name: "name", Type: tftypes.String, Required: true},
			{Name: "id", Type: tftypes.String, Computed: true},
		},
	}}
}

func (captureProvider) ValidateDataSourceConfig(context.Context, *tfprotov5.ValidateDataSourceConfigRequest) (*tfprotov5.ValidateDataSourceConfigResponse, error) {
	return &tfprotov5.ValidateDataSourceConfigResponse{}, nil
}

func (captureProvider) ReadDataSource(_ context.Context, request *tfprotov5.ReadDataSourceRequest) (*tfprotov5.ReadDataSourceResponse, error) {
	if request == nil || request.Config == nil {
		return nil, fmt.Errorf("capture provider received an empty data-source configuration")
	}
	config, err := request.Config.Unmarshal(captureItemSchema().ValueType())
	if err != nil {
		return nil, fmt.Errorf("decode capture data-source configuration: %w", err)
	}
	var attributes map[string]tftypes.Value
	if err := config.As(&attributes); err != nil {
		return nil, fmt.Errorf("read capture data-source configuration: %w", err)
	}
	var name string
	if err := attributes["name"].As(&name); err != nil {
		return nil, fmt.Errorf("read capture data-source name: %w", err)
	}

	idVersion := os.Getenv("INFRAWRIGHT_CAPTURE_ID_VERSION")
	digest := sha256.Sum256([]byte(idVersion + "\x00" + name))
	id := hex.EncodeToString(digest[:8])
	value := tftypes.NewValue(captureItemSchema().ValueType(), map[string]tftypes.Value{
		"name": tftypes.NewValue(tftypes.String, name),
		"id":   tftypes.NewValue(tftypes.String, id),
	})
	state, err := tfprotov5.NewDynamicValue(captureItemSchema().ValueType(), value)
	if err != nil {
		return nil, fmt.Errorf("encode capture data-source state: %w", err)
	}
	return &tfprotov5.ReadDataSourceResponse{State: &state}, nil
}
