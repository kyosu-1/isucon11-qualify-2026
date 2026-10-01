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

	condMu sync.RWMutex
	conds  []cond // timestamp 昇順・重複なし
}

// メモリ上のコンディション 1 件。condition 文字列は 3 つのフラグから復元できるので持たない
type cond struct {
	ts      int64
	sitting bool
	flags   uint8
	msg     string
}

const (
	flagDirty      = 1 << 0
	flagOverweight = 1 << 1
	flagBroken     = 1 << 2
)

var (
	conditionStrings [8]string
	flagLevels       [8]string
	flagBadCount     [8]int
)

func init() {
	b := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	for f := 0; f < 8; f++ {
		conditionStrings[f] = "is_dirty=" + b(f&flagDirty != 0) + ",is_overweight=" + b(f&flagOverweight != 0) + ",is_broken=" + b(f&flagBroken != 0)
		n := f&1 + f>>1&1 + f>>2&1
		flagBadCount[f] = n
		switch n {
		case 0:
			flagLevels[f] = conditionLevelInfo
		case 3:
			flagLevels[f] = conditionLevelCritical
		default:
			flagLevels[f] = conditionLevelWarning
		}
	}
}

// "is_dirty=...,is_overweight=...,is_broken=..." を厳密に解釈する（isValidConditionFormat と同じ受理条件）
func parseCondition(s string) (uint8, bool) {
	for f := 0; f < 8; f++ {
		if s == conditionStrings[f] {
			return uint8(f), true
		}
	}
	return 0, false
}

// メッセージは種類が少ないので同じ文字列を共有する
var (
	internMu sync.RWMutex
	internM  = map[string]string{}
)

func intern(s string) string {
	internMu.RLock()
	v, ok := internM[s]
	internMu.RUnlock()
	if ok {
		return v
	}
	internMu.Lock()
	if len(internM) < 100000 {
		internM[s] = s
	}
	internMu.Unlock()
	return s
}

// コンディションを ISU に追加する（通常は時刻順に届くので末尾追加）
func (i *isuInfo) addConds(cs []cond) {
	i.condMu.Lock()
	for _, c := range cs {
		n := len(i.conds)
		if n == 0 || i.conds[n-1].ts < c.ts {
			i.conds = append(i.conds, c)
			continue
		}
		pos := sort.Search(n, func(k int) bool { return i.conds[k].ts >= c.ts })
		if i.conds[pos].ts == c.ts {
			continue
		}
		i.conds = append(i.conds, cond{})
		copy(i.conds[pos+1:], i.conds[pos:])
		i.conds[pos] = c
	}
	i.condMu.Unlock()
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
	// 全コンディションを主キー順（uuid, timestamp）に読む
	rows, err := db.Queryx("SELECT `jia_isu_uuid`, `timestamp`, `is_sitting`, `condition`, `message` FROM `isu_condition` ORDER BY `jia_isu_uuid`, `timestamp`")
	if err != nil {
		return err
	}
	defer rows.Close()
	var cur *isuInfo
	for rows.Next() {
		var c IsuCondition
		if err := rows.Scan(&c.JIAIsuUUID, &c.Timestamp, &c.IsSitting, &c.Condition, &c.Message); err != nil {
			return err
		}
		if cur == nil || cur.UUID != c.JIAIsuUUID {
			cur = byUUID[c.JIAIsuUUID]
		}
		flags, ok := parseCondition(c.Condition)
		if cur == nil || !ok {
			continue
		}
		cur.conds = append(cur.conds, cond{ts: c.Timestamp.Unix(), sitting: c.IsSitting, flags: flags, msg: intern(c.Message)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, i := range byUUID {
		if n := len(i.conds); n > 0 {
			l := i.conds[n-1]
			i.latest.Store(&latestCond{Timestamp: l.ts, IsSitting: l.sitting, Condition: conditionStrings[l.flags], Message: l.msg, Level: flagLevels[l.flags]})
		}
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
