package cmd

import (
	"fmt"

	"github.com/MetrolistGroup/metrobot/db"
)

type AdminHandler struct {
	DB *db.DB
}

func (h *AdminHandler) AddAdmin(banner PlatformBanner, callerID, targetID string, cfg db.PermaAdminProvider) (string, error) {
	platform := banner.Platform()
	if !h.DB.IsPermaAdmin(platform, callerID, cfg) {
		return "Only permanent admins can add admins.", nil
	}

	if err := h.DB.AddAdmin(platform, targetID, callerID); err != nil {
		return "", fmt.Errorf("adding admin: %w", err)
	}

	return fmt.Sprintf("%s has been added as an admin.", formatUserRef(banner, targetID)), nil
}

func (h *AdminHandler) RemoveAdmin(banner PlatformBanner, callerID, targetID string, cfg db.PermaAdminProvider) (string, error) {
	platform := banner.Platform()
	if !h.DB.IsPermaAdmin(platform, callerID, cfg) {
		return "Only permanent admins can remove admins.", nil
	}

	if h.DB.IsPermaAdmin(platform, targetID, cfg) {
		return "Cannot remove a permanent admin.", nil
	}

	if err := h.DB.RemoveAdmin(platform, targetID); err != nil {
		return "", fmt.Errorf("removing admin: %w", err)
	}

	return fmt.Sprintf("%s has been removed as an admin.", formatUserRef(banner, targetID)), nil
}
