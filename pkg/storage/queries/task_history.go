package queries

import (
	"context"
	"fmt"

	"github.com/LumeraProtocol/supernode/v2/pkg/logtrace"
	"github.com/LumeraProtocol/supernode/v2/pkg/types"

	json "github.com/json-iterator/go"
)

type TaskHistoryQueries interface {
	InsertTaskHistory(history types.TaskHistory) (int, error)
	QueryTaskHistory(taskID string) (history []types.TaskHistory, err error)
}

// InsertTaskHistory inserts task history
func (s *SQLiteStore) InsertTaskHistory(history types.TaskHistory) (hID int, err error) {
	var stringifyDetails string
	if history.Details != nil {
		stringifyDetails = history.Details.Stringify()
	}

	const insertQuery = "INSERT INTO task_history(id, time, task_id, status, details) VALUES(NULL,?,?,?,?);"
	res, err := s.db.Exec(insertQuery, history.CreatedAt, history.TaskID, history.Status, stringifyDetails)

	if err != nil {
		return 0, err
	}

	var id int64
	if id, err = res.LastInsertId(); err != nil {
		return 0, err
	}

	return int(id), nil
}

// QueryTaskHistory gets task history by taskID
func (s *SQLiteStore) QueryTaskHistory(taskID string) (history []types.TaskHistory, err error) {
	const selectQuery = "SELECT * FROM task_history WHERE task_id = ? LIMIT 100"
	rows, err := s.db.Query(selectQuery, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var data []types.TaskHistory
	for rows.Next() {
		i := types.TaskHistory{}
		var details string
		err = rows.Scan(&i.ID, &i.CreatedAt, &i.TaskID, &i.Status, &details)
		if err != nil {
			return nil, err
		}

		if details != emptyString {
			err = json.Unmarshal([]byte(details), &i.Details)
			if err != nil {

				logtrace.Info(context.Background(), "Detals", logtrace.Fields{"details": details})
				logtrace.Error(context.Background(), fmt.Sprintf("cannot unmarshal task history details: %s", details), logtrace.Fields{"error": err})
				i.Details = nil
			}
		}

		data = append(data, i)
	}

	return data, nil
}
