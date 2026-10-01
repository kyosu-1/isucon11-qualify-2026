package main

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/gommon/log"
)

// アプリ内キャッシュ。DB が正で、起動時と POST /initialize で DB から作り直す。
//   - ISU の属性（id / name / character / 所有者）と最新コンディション
//   - 登録済みユーザー
// コンディションの書き込みはメモリのバッファに積み、condFlushInterval ごとにまとめて INSERT する
// （反映の遅れはマニュアルで許容されている）。

type latestCond struct {
	Timestamp int64
	IsSitting bool
	Condition string
	Message   string
	Level     string
}

type isuInfo struct {
	ID        int
	UUID      string
	Name      string
	Character string
	UserID    string
	latest    atomic.Pointer[latestCond]
}

var (
	isuMu     sync.RWMutex
	isuByUUID = map[string]*isuInfo{}
	isuByUser = map[string][]*isuInfo{} // id 降順

	userSet atomic.Pointer[sync.Map]
)

func init() {
	userSet.Store(&sync.Map{})
}

func getIsu(uuid string) *isuInfo {
	isuMu.RLock()
	i := isuByUUID[uuid]
	isuMu.RUnlock()
	return i
}

func addIsu(i *isuInfo) {
	isuMu.Lock()
	isuByUUID[i.UUID] = i
	l := append(isuByUser[i.UserID], i)
	sort.Slice(l, func(a, b int) bool { return l[a].ID > l[b].ID })
	isuByUser[i.UserID] = l
	isuMu.Unlock()
}

// DB からキャッシュを作り直す
func loadCache() error {
	isus := []struct {
		ID        int    `db:"id"`
		UUID      string `db:"jia_isu_uuid"`
		Name      string `db:"name"`
		Character string `db:"character"`
		UserID    string `db:"jia_user_id"`
	}{}
	if err := db.Select(&isus, "SELECT `id`, `jia_isu_uuid`, `name`, COALESCE(`character`, '') AS `character`, `jia_user_id` FROM `isu`"); err != nil {
		return err
	}
	conds := []IsuCondition{}
	if err := db.Select(&conds, "SELECT c.`jia_isu_uuid`, c.`timestamp`, c.`is_sitting`, c.`condition`, c.`message` FROM `isu_condition` c"+
		" JOIN (SELECT `jia_isu_uuid`, MAX(`timestamp`) AS ts FROM `isu_condition` GROUP BY `jia_isu_uuid`) m"+
		" ON m.`jia_isu_uuid` = c.`jia_isu_uuid` AND m.ts = c.`timestamp`"); err != nil {
		return err
	}
	users := []string{}
	if err := db.Select(&users, "SELECT `jia_user_id` FROM `user`"); err != nil {
		return err
	}

	byUUID := make(map[string]*isuInfo, len(isus))
	byUser := map[string][]*isuInfo{}
	for _, r := range isus {
		i := &isuInfo{ID: r.ID, UUID: r.UUID, Name: r.Name, Character: r.Character, UserID: r.UserID}
		byUUID[i.UUID] = i
		byUser[i.UserID] = append(byUser[i.UserID], i)
	}
	for _, l := range byUser {
		l := l
		sort.Slice(l, func(a, b int) bool { return l[a].ID > l[b].ID })
	}
	for _, c := range conds {
		i, ok := byUUID[c.JIAIsuUUID]
		if !ok {
			continue
		}
		level, err := calculateConditionLevel(c.Condition)
		if err != nil {
			continue
		}
		i.latest.Store(&latestCond{Timestamp: c.Timestamp.Unix(), IsSitting: c.IsSitting, Condition: c.Condition, Message: c.Message, Level: level})
	}
	us := &sync.Map{}
	for _, u := range users {
		us.Store(u, struct{}{})
	}

	isuMu.Lock()
	isuByUUID = byUUID
	isuByUser = byUser
	isuMu.Unlock()
	userSet.Store(us)
	return nil
}

// ---- コンディションの書き込みバッファ ----

type condRow struct {
	UUID      string
	Timestamp time.Time
	IsSitting bool
	Condition string
	Message   string
}

const (
	condFlushInterval = 200 * time.Millisecond
	condInsertChunk   = 2000
)

var (
	condMu  sync.Mutex
	condBuf []condRow
)

func enqueueConditions(rows []condRow) {
	condMu.Lock()
	condBuf = append(condBuf, rows...)
	condMu.Unlock()
}

func takeConditions() []condRow {
	condMu.Lock()
	rows := condBuf
	condBuf = nil
	condMu.Unlock()
	return rows
}

func condFlushLoop() {
	for {
		time.Sleep(condFlushInterval)
		flushConditions(takeConditions())
	}
}

func flushConditions(rows []condRow) {
	for len(rows) > 0 {
		n := len(rows)
		if n > condInsertChunk {
			n = condInsertChunk
		}
		chunk := rows[:n]
		rows = rows[n:]

		var sb strings.Builder
		sb.WriteString("INSERT IGNORE INTO `isu_condition` (`jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message`) VALUES ")
		args := make([]interface{}, 0, len(chunk)*5)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString("(?,?,?,?,?)")
			args = append(args, r.UUID, r.Timestamp, r.IsSitting, r.Condition, r.Message)
		}
		if _, err := db.Exec(sb.String(), args...); err != nil {
			log.Errorf("flushConditions: %v (%d rows dropped)", err, len(chunk))
		}
	}
}
