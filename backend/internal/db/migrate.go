package db

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

// Schema is the PostgreSQL schema that holds Ebb's decision-layer tables.
const Schema = "ebb"

// ConfigureEbb points a pool config at the ebb schema (falling back to public for shared objects).
func ConfigureEbb(cfg *pgxpool.Config) {
	cfg.ConnConfig.RuntimeParams["search_path"] = Schema + ",public"
}

// Migrate creates the ebb schema and its tables if they do not exist yet.
func Migrate(ctx context.Context, p *pgxpool.Pool) error {
	if _, err := p.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+Schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	if _, err := p.Exec(ctx, schemaSQL); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
