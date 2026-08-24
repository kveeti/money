package endpoints

import (
	"money/backend/state"
	"net/http"
)

func HandleGetMe(state *state.State, getUserID GetUserID) Handler {
	return func(w http.ResponseWriter, r *http.Request) error {
		userID, err := getUserID(r)
		if err != nil {
			return err
		}

		user, err := state.Data.GetUserByID(r.Context(), userID)
		if err != nil {
			return NewUnexpectedErr("error getting user: %w", err)
		}
		if user == nil {
			return NewErr("user not found", http.StatusNotFound)
		}

		Json(w, JSON{
			"id":                       user.ID,
			"email":                    user.Email,
			"home_currency":            user.HomeCurrency,
			"enable_banking_available": state.Config.EnableBanking.Enabled() && state.Config.EnableBanking.AllowsSubject(user.Subject),
		})
		return nil
	}
}
