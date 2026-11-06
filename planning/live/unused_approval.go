package live

import (
	"errors"
	"time"

	"gorm.io/gorm"
	"nofx/planning"
)

// Callers hold the run lock. Only an approval with no persisted submission
// intent can be retired locally; exchange orders are never touched here.
func retireUnusedApproval(tx *gorm.DB, v View, state, reason string) error {
	if v.State != "approved" || v.StartedAt != nil || len(v.Orders) != 0 {
		return errors.New("This trade has started. Verify its exchange orders before choosing another entry.")
	}
	res := tx.Model(&Run{}).Where("id = ? AND user_id = ? AND state = ? AND started_at IS NULL AND orders_json = ?", v.ID, v.UserID, "approved", v.OrdersJSON).
		Updates(map[string]any{"state": state, "error": reason, "updated_at": time.Now().UTC()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errors.New("The previous approval changed. Refresh trade status before continuing.")
	}
	if err := tx.Where("account_key = ? AND run_id = ? AND user_id = ?", v.AccountKey, v.ID, v.UserID).Delete(&Lease{}).Error; err != nil {
		return err
	}
	return tx.Model(&planning.Draft{}).Where("user_id = ? AND id = ?", v.UserID, v.PlanID).Update("status", state).Error
}

func (c *Controller) retireUnusedApproval(v *View, state, reason string) error {
	if err := c.DB.Transaction(func(tx *gorm.DB) error {
		return retireUnusedApproval(tx, *v, state, reason)
	}); err != nil {
		return err
	}
	v.State, v.Error, v.UpdatedAt = state, reason, time.Now().UTC()
	return nil
}
