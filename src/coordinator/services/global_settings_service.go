// Copyright (C) 2025 Homer Server Contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sipcapture/homer-core/src/coordinator/sqlvalidator"
)

// GlobalSetting represents a row from global_settings (system-wide key/value config).
type GlobalSetting struct {
	ID       int64           `json:"id"`
	GUID     string          `json:"guid"`
	PartID   int             `json:"partid"`
	Category string          `json:"category"`
	Param    string          `json:"param"`
	Data     json.RawMessage `json:"data"`
}

type GlobalSettingsListFilters struct {
	Category string
	Param    string
	Limit    int
}

// GlobalSettingsService provides CRUD access to global_settings.
type GlobalSettingsService struct {
	db *sql.DB
}

func NewGlobalSettingsService(db *sql.DB) *GlobalSettingsService {
	return &GlobalSettingsService{db: db}
}

func (s *GlobalSettingsService) List(ctx context.Context, filters GlobalSettingsListFilters) ([]GlobalSetting, error) {
	if s.db == nil {
		return nil, fmt.Errorf("settings db not available")
	}
	where := "1=1"
	if filters.Category != "" {
		value := "%" + sqlvalidator.SafeString(filters.Category) + "%"
		where += fmt.Sprintf(" AND category ILIKE '%s'", value)
	}
	if filters.Param != "" {
		value := "%" + sqlvalidator.SafeString(filters.Param) + "%"
		where += fmt.Sprintf(" AND param ILIKE '%s'", value)
	}
	limit := filters.Limit
	if limit <= 0 {
		limit = 100
	}
	query := fmt.Sprintf(
		`SELECT id, guid, partid, category, param, data
		 FROM global_settings
		 WHERE %s
		 ORDER BY id
		 LIMIT %d`,
		where, limit,
	)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]GlobalSetting, 0)
	for rows.Next() {
		item, err := scanGlobalSetting(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *GlobalSettingsService) GetByGUID(ctx context.Context, guid string) (*GlobalSetting, error) {
	if s.db == nil {
		return nil, fmt.Errorf("settings db not available")
	}
	query := `SELECT id, guid, partid, category, param, data
		 FROM global_settings WHERE guid = $1 LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, sqlvalidator.SanitizeBoundString(guid))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	item, err := scanGlobalSetting(rows)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *GlobalSettingsService) Create(ctx context.Context, item GlobalSetting) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("settings db not available")
	}
	if item.GUID == "" {
		item.GUID = newGUID()
	}
	if item.PartID <= 0 {
		item.PartID = 10
	}
	dataStr := "{}"
	if len(item.Data) > 0 {
		dataStr = string(item.Data)
	}
	insertSQL := `INSERT INTO global_settings (guid, partid, category, param, data, create_date)
		 VALUES ($1, $2, $3, $4, $5, current_timestamp)
		 RETURNING guid`
	var guid string
	queryRow := s.db.QueryRowContext
	if isPostgresSettings(s.db) {
		queryRow = postgresQueryRow(s.db)
	}
	if err := queryRow(ctx, insertSQL, sqlvalidator.SanitizeBoundString(item.GUID), item.PartID, sqlvalidator.SanitizeBoundString(item.Category), sqlvalidator.SanitizeBoundString(item.Param), jsonDataParam(dataStr)).Scan(&guid); err != nil {
		return item.GUID, err
	}
	return guid, nil
}

func (s *GlobalSettingsService) Update(ctx context.Context, guid string, item GlobalSetting) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("settings db not available")
	}
	if item.PartID <= 0 {
		item.PartID = 10
	}
	dataStr := "{}"
	if len(item.Data) > 0 {
		dataStr = string(item.Data)
	}
	updateSQL := `UPDATE global_settings
		 SET partid = $1, category = $2, param = $3, data = $4
		 WHERE guid = $5
		 RETURNING guid`
	var updated string
	queryRow := s.db.QueryRowContext
	if isPostgresSettings(s.db) {
		queryRow = postgresQueryRow(s.db)
	}
	if err := queryRow(ctx, updateSQL, item.PartID, sqlvalidator.SanitizeBoundString(item.Category), sqlvalidator.SanitizeBoundString(item.Param), jsonDataParam(dataStr), sqlvalidator.SanitizeBoundString(guid)).Scan(&updated); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return updated, nil
}

func (s *GlobalSettingsService) Delete(ctx context.Context, guid string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("settings db not available")
	}
	deleteSQL := `DELETE FROM global_settings WHERE guid = $1 RETURNING id`
	var id int64
	queryRow := s.db.QueryRowContext
	if isPostgresSettings(s.db) {
		queryRow = postgresQueryRow(s.db)
	}
	if err := queryRow(ctx, deleteSQL, sqlvalidator.SanitizeBoundString(guid)).Scan(&id); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func scanGlobalSetting(rows *sql.Rows) (GlobalSetting, error) {
	var (
		item     GlobalSetting
		id       sql.NullInt64
		guid     sql.NullString
		partid   sql.NullInt64
		category sql.NullString
		param    sql.NullString
		data     interface{}
	)
	if err := rows.Scan(&id, &guid, &partid, &category, &param, &data); err != nil {
		return item, err
	}
	if id.Valid {
		item.ID = id.Int64
	}
	if guid.Valid {
		item.GUID = guid.String
	}
	if partid.Valid {
		item.PartID = int(partid.Int64)
	} else {
		item.PartID = 10
	}
	if category.Valid {
		item.Category = category.String
	}
	if param.Valid {
		item.Param = param.String
	}
	switch v := data.(type) {
	case []byte:
		if len(v) > 0 {
			item.Data = json.RawMessage(append([]byte(nil), v...))
		}
	case string:
		if v != "" {
			item.Data = json.RawMessage(v)
		}
	case map[string]interface{}, []interface{}:
		if b, err := json.Marshal(v); err == nil {
			item.Data = json.RawMessage(b)
		}
	case nil:
		item.Data = json.RawMessage("{}")
	default:
		if b, err := json.Marshal(v); err == nil {
			item.Data = json.RawMessage(b)
		}
	}
	if len(item.Data) == 0 {
		item.Data = json.RawMessage("{}")
	}
	return item, nil
}
