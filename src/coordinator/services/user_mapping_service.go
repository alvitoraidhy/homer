// Copyright (C) 2025 Homer Server Contributors
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sipcapture/homer-core/src/coordinator/sqlvalidator"
)

// UserMappingService stores per-user protocol field overrides (widget column order/selection).
// Backed by user_mapping_settings.
type UserMappingService struct {
	db *sql.DB
}

func NewUserMappingService(db *sql.DB) *UserMappingService {
	return &UserMappingService{db: db}
}

// ListForUser returns all mapping overrides for the user as UserSetting rows (category=user_mapping).
func (s *UserMappingService) ListForUser(ctx context.Context, username string) ([]UserSetting, error) {
	if s.db == nil {
		return nil, fmt.Errorf("settings db not available")
	}
	query := `SELECT id, guid, username, partid, mapping_key, data
		 FROM user_mapping_settings
		 WHERE username = $1
		 ORDER BY id`
	rows, err := s.db.QueryContext(ctx, query, sqlvalidator.SanitizeBoundString(username))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UserSetting, 0)
	for rows.Next() {
		setting, err := scanUserMappingSettingRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, setting)
	}
	return out, rows.Err()
}

// Get returns one override by storage key "{hepid}_{profile}" (param).
func (s *UserMappingService) Get(ctx context.Context, username, mappingKey string) (*UserSetting, error) {
	if s.db == nil {
		return nil, fmt.Errorf("settings db not available")
	}
	query := `SELECT id, guid, username, partid, mapping_key, data
		 FROM user_mapping_settings
		 WHERE username = $1 AND mapping_key = $2
		 LIMIT 1`
	rows, err := s.db.QueryContext(ctx, query, sqlvalidator.SanitizeBoundString(username), sqlvalidator.SanitizeBoundString(mappingKey))
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
	setting, err := scanUserMappingSettingRow(rows)
	if err != nil {
		return nil, err
	}
	return &setting, nil
}

// Upsert inserts or updates by username + mapping_key.
func (s *UserMappingService) Upsert(ctx context.Context, username, mappingKey string, data json.RawMessage, partid int) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("settings db not available")
	}
	if partid <= 0 {
		partid = 10
	}
	dataStr := string(data)
	updateSQL := `UPDATE user_mapping_settings
		 SET data = $1, partid = $2
		 WHERE username = $3 AND mapping_key = $4
		 RETURNING guid`
	var updatedGUID string
	queryRow := s.db.QueryRowContext
	if isPostgresSettings(s.db) {
		queryRow = postgresQueryRow(s.db)
	}
	if err := queryRow(ctx, updateSQL, jsonDataParam(dataStr), partid, sqlvalidator.SanitizeBoundString(username), sqlvalidator.SanitizeBoundString(mappingKey)).Scan(&updatedGUID); err == nil {
		return updatedGUID, nil
	} else if err != sql.ErrNoRows {
		return "", err
	}

	guid := newGUID()
	insertSQL := `INSERT INTO user_mapping_settings (guid, username, partid, mapping_key, data, create_date)
		 VALUES ($1, $2, $3, $4, $5, current_timestamp)
		 RETURNING guid`
	var inserted string
	if err := queryRow(ctx, insertSQL, sqlvalidator.SanitizeBoundString(guid), sqlvalidator.SanitizeBoundString(username), partid, sqlvalidator.SanitizeBoundString(mappingKey), jsonDataParam(dataStr)).Scan(&inserted); err != nil {
		return guid, err
	}
	if inserted != "" {
		return inserted, nil
	}
	return guid, nil
}

// Delete removes override for username + mapping_key.
func (s *UserMappingService) Delete(ctx context.Context, username, mappingKey string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("settings db not available")
	}
	query := `DELETE FROM user_mapping_settings
		 WHERE username = $1 AND mapping_key = $2
		 RETURNING id`
	var deletedID int64
	queryRow := s.db.QueryRowContext
	if isPostgresSettings(s.db) {
		queryRow = postgresQueryRow(s.db)
	}
	if err := queryRow(ctx, query, sqlvalidator.SanitizeBoundString(username), sqlvalidator.SanitizeBoundString(mappingKey)).Scan(&deletedID); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func scanUserMappingSettingRow(rows *sql.Rows) (UserSetting, error) {
	var (
		setting    UserSetting
		id         sql.NullInt64
		guid       sql.NullString
		username   sql.NullString
		partid     sql.NullInt64
		mappingKey sql.NullString
		data       interface{}
	)
	if err := rows.Scan(&id, &guid, &username, &partid, &mappingKey, &data); err != nil {
		return setting, err
	}
	if id.Valid {
		setting.ID = id.Int64
	}
	if guid.Valid {
		setting.GUID = guid.String
	}
	if username.Valid {
		setting.UserName = username.String
	}
	if partid.Valid {
		setting.PartID = int(partid.Int64)
	} else {
		setting.PartID = 10
	}
	setting.Category = "user_mapping"
	if mappingKey.Valid {
		setting.Param = mappingKey.String
	}
	setting.Data = rawMessageFromSQLValue(data)
	return setting, nil
}
