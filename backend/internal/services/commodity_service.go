package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/user/antimoney/internal/models"
)

type CommodityService struct {
	pool *pgxpool.Pool
}

func NewCommodityService(pool *pgxpool.Pool) *CommodityService {
	return &CommodityService{pool: pool}
}

type CreateCommodityRequest struct {
	Namespace string `json:"namespace"`
	Mnemonic  string `json:"mnemonic"`
	Fullname  string `json:"fullname"`
	Fraction  int    `json:"fraction"`
}

func (s *CommodityService) CreateCommodity(ctx context.Context, req CreateCommodityRequest) (*models.Commodity, error) {
	guid := uuid.New().String()
	now := time.Now().UTC()
	ns := req.Namespace
	if ns == "" {
		ns = "CURRENCY"
	}
	frac := req.Fraction
	if frac == 0 {
		frac = 100
	}

	commodity := &models.Commodity{
		GUID:      guid,
		Namespace: ns,
		Mnemonic:  req.Mnemonic,
		Fullname:  req.Fullname,
		Fraction:  frac,
		CreatedAt: now,
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO commodities (guid, namespace, mnemonic, fullname, fraction, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		commodity.GUID, commodity.Namespace, commodity.Mnemonic, commodity.Fullname,
		commodity.Fraction, commodity.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert commodity: %w", err)
	}

	return commodity, nil
}

func (s *CommodityService) ListCommodities(ctx context.Context) ([]models.Commodity, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT guid, namespace, mnemonic, fullname, fraction, created_at
		 FROM commodities ORDER BY namespace, mnemonic`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commodities []models.Commodity
	for rows.Next() {
		var c models.Commodity
		if err := rows.Scan(&c.GUID, &c.Namespace, &c.Mnemonic, &c.Fullname, &c.Fraction, &c.CreatedAt); err != nil {
			return nil, err
		}
		commodities = append(commodities, c)
	}
	return commodities, nil
}

func (s *CommodityService) GetCommodity(ctx context.Context, guid string) (*models.Commodity, error) {
	c := &models.Commodity{}
	err := s.pool.QueryRow(ctx,
		`SELECT guid, namespace, mnemonic, fullname, fraction, created_at
		 FROM commodities WHERE guid = $1`, guid,
	).Scan(&c.GUID, &c.Namespace, &c.Mnemonic, &c.Fullname, &c.Fraction, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

func (s *CommodityService) DeleteCommodity(ctx context.Context, guid string) error {
	cmd, err := s.pool.Exec(ctx, `DELETE FROM commodities WHERE guid = $1`, guid)
	if err != nil {
		// likely foreign key violation if deleting a commodity that is still in use
		return fmt.Errorf("delete commodity: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
