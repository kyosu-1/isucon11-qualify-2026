package main

import (
	"bytes"
	"crypto/ecdsa"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/go-sql-driver/mysql"
	"github.com/gorilla/sessions"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

const (
	sessionName                 = "isucondition_go"
	conditionLimit              = 20
	frontendContentsPath        = "../public"
	jiaJWTSigningKeyPath        = "../ec256-public.pem"
	defaultIconFilePath         = "../NoImage.jpg"
	defaultJIAServiceURL        = "http://localhost:5000"
	mysqlErrNumDuplicateEntry   = 1062
	conditionLevelInfo          = "info"
	conditionLevelWarning       = "warning"
	conditionLevelCritical      = "critical"
	scoreConditionLevelInfo     = 3
	scoreConditionLevelWarning  = 2
	scoreConditionLevelCritical = 1
)

var (
	db                  *sqlx.DB
	sessionStore        sessions.Store
	mySQLConnectionData *MySQLConnectionEnv

	jiaJWTSigningKey *ecdsa.PublicKey

	postIsuConditionTargetBaseURL string // JIAへのactivate時に登録する，ISUがconditionを送る先のURL
)

type Config struct {
	Name string `db:"name"`
	URL  string `db:"url"`
}

type Isu struct {
	ID         int       `db:"id" json:"id"`
	JIAIsuUUID string    `db:"jia_isu_uuid" json:"jia_isu_uuid"`
	Name       string    `db:"name" json:"name"`
	Image      []byte    `db:"image" json:"-"`
	Character  string    `db:"character" json:"character"`
	JIAUserID  string    `db:"jia_user_id" json:"-"`
	CreatedAt  time.Time `db:"created_at" json:"-"`
	UpdatedAt  time.Time `db:"updated_at" json:"-"`
}

type IsuFromJIA struct {
	Character string `json:"character"`
}

type GetIsuListResponse struct {
	ID                 int                      `json:"id"`
	JIAIsuUUID         string                   `json:"jia_isu_uuid"`
	Name               string                   `json:"name"`
	Character          string                   `json:"character"`
	LatestIsuCondition *GetIsuConditionResponse `json:"latest_isu_condition"`
}

type IsuCondition struct {
	ID         int       `db:"id"`
	JIAIsuUUID string    `db:"jia_isu_uuid"`
	Timestamp  time.Time `db:"timestamp"`
	IsSitting  bool      `db:"is_sitting"`
	Condition  string    `db:"condition"`
	Message    string    `db:"message"`
	CreatedAt  time.Time `db:"created_at"`
}

type MySQLConnectionEnv struct {
	Host     string
	Port     string
	User     string
	DBName   string
	Password string
}

type InitializeRequest struct {
	JIAServiceURL string `json:"jia_service_url"`
}

type InitializeResponse struct {
	Language string `json:"language"`
}

type GetMeResponse struct {
	JIAUserID string `json:"jia_user_id"`
}

type GraphResponse struct {
	StartAt             int64           `json:"start_at"`
	EndAt               int64           `json:"end_at"`
	Data                *GraphDataPoint `json:"data"`
	ConditionTimestamps []int64         `json:"condition_timestamps"`
}

type GraphDataPoint struct {
	Score      int                  `json:"score"`
	Percentage ConditionsPercentage `json:"percentage"`
}

type ConditionsPercentage struct {
	Sitting      int `json:"sitting"`
	IsBroken     int `json:"is_broken"`
	IsDirty      int `json:"is_dirty"`
	IsOverweight int `json:"is_overweight"`
}

type GraphDataPointWithInfo struct {
	JIAIsuUUID          string
	StartAt             time.Time
	Data                GraphDataPoint
	ConditionTimestamps []int64
}

type GetIsuConditionResponse struct {
	JIAIsuUUID     string `json:"jia_isu_uuid"`
	IsuName        string `json:"isu_name"`
	Timestamp      int64  `json:"timestamp"`
	IsSitting      bool   `json:"is_sitting"`
	Condition      string `json:"condition"`
	ConditionLevel string `json:"condition_level"`
	Message        string `json:"message"`
}

type TrendResponse struct {
	Character string            `json:"character"`
	Info      []*TrendCondition `json:"info"`
	Warning   []*TrendCondition `json:"warning"`
	Critical  []*TrendCondition `json:"critical"`
}

type TrendCondition struct {
	ID        int   `json:"isu_id"`
	Timestamp int64 `json:"timestamp"`
}

type PostIsuConditionRequest struct {
	IsSitting bool   `json:"is_sitting"`
	Condition string `json:"condition"`
	Message   string `json:"message"`
	Timestamp int64  `json:"timestamp"`
}

type JIAServiceRequest struct {
	TargetBaseURL string `json:"target_base_url"`
	IsuUUID       string `json:"isu_uuid"`
}

func getEnv(key string, defaultValue string) string {
	val := os.Getenv(key)
	if val != "" {
		return val
	}
	return defaultValue
}

func NewMySQLConnectionEnv() *MySQLConnectionEnv {
	return &MySQLConnectionEnv{
		Host:     getEnv("MYSQL_HOST", "127.0.0.1"),
		Port:     getEnv("MYSQL_PORT", "3306"),
		User:     getEnv("MYSQL_USER", "isucon"),
		DBName:   getEnv("MYSQL_DBNAME", "isucondition"),
		Password: getEnv("MYSQL_PASS", "isucon"),
	}
}

func (mc *MySQLConnectionEnv) ConnectDB() (*sqlx.DB, error) {
	dsn := fmt.Sprintf("%v:%v@tcp(%v:%v)/%v?parseTime=true&loc=Asia%%2FTokyo&interpolateParams=true", mc.User, mc.Password, mc.Host, mc.Port, mc.DBName)
	return sqlx.Open("mysql", dsn)
}

func init() {
	sessionStore = sessions.NewCookieStore([]byte(getEnv("SESSION_KEY", "isucondition")))

	key, err := ioutil.ReadFile(jiaJWTSigningKeyPath)
	if err != nil {
		log.Fatalf("failed to read file: %v", err)
	}
	jiaJWTSigningKey, err = jwt.ParseECPublicKeyFromPEM(key)
	if err != nil {
		log.Fatalf("failed to parse ECDSA public key: %v", err)
	}
}

func main() {
	e := echo.New()
	e.Logger.SetLevel(log.ERROR)

	e.Use(middleware.Recover())

	e.POST("/initialize", postInitialize)

	e.POST("/api/auth", postAuthentication)
	e.POST("/api/signout", postSignout)
	e.GET("/api/user/me", getMe)
	e.GET("/api/isu", getIsuList)
	e.POST("/api/isu", postIsu)
	e.GET("/api/isu/:jia_isu_uuid", getIsuID)
	e.GET("/api/isu/:jia_isu_uuid/icon", getIsuIcon)
	e.GET("/api/isu/:jia_isu_uuid/graph", getIsuGraph)
	e.GET("/api/condition/:jia_isu_uuid", getIsuConditions)
	e.GET("/api/trend", getTrend)

	e.POST("/api/condition/:jia_isu_uuid", postIsuCondition)

	e.GET("/", getIndex)
	e.GET("/isu/:jia_isu_uuid", getIndex)
	e.GET("/isu/:jia_isu_uuid/condition", getIndex)
	e.GET("/isu/:jia_isu_uuid/graph", getIndex)
	e.GET("/register", getIndex)
	e.Static("/assets", frontendContentsPath+"/assets")

	mySQLConnectionData = NewMySQLConnectionEnv()

	var err error
	db, err = mySQLConnectionData.ConnectDB()
	if err != nil {
		e.Logger.Fatalf("failed to connect db: %v", err)
		return
	}
	db.SetMaxOpenConns(40)
	db.SetMaxIdleConns(40)
	defer db.Close()

	// DB が別ノードでも起動順序に依存しないよう、繋がるまで待ってからキャッシュを作る
	for i := 0; ; i++ {
		if err = loadCache(); err == nil {
			break
		}
		if i >= 120 {
			e.Logger.Fatalf("failed to load cache: %v", err)
			return
		}
		time.Sleep(time.Second)
	}
	go condFlushLoop()

	postIsuConditionTargetBaseURL = os.Getenv("POST_ISUCONDITION_TARGET_BASE_URL")
	if postIsuConditionTargetBaseURL == "" {
		e.Logger.Fatalf("missing: POST_ISUCONDITION_TARGET_BASE_URL")
		return
	}

	go trendLoop()

	serverPort := fmt.Sprintf(":%v", getEnv("SERVER_APP_PORT", "3000"))
	e.Logger.Fatal(e.Start(serverPort))
}

func getSession(r *http.Request) (*sessions.Session, error) {
	session, err := sessionStore.Get(r, sessionName)
	if err != nil {
		return nil, err
	}
	return session, nil
}

func getUserIDFromSession(c echo.Context) (string, int, error) {
	session, err := getSession(c.Request())
	if err != nil {
		return "", http.StatusInternalServerError, fmt.Errorf("failed to get session: %v", err)
	}
	_jiaUserID, ok := session.Values["jia_user_id"]
	if !ok {
		return "", http.StatusUnauthorized, fmt.Errorf("no session")
	}

	jiaUserID := _jiaUserID.(string)
	if _, ok := userSet.Load().Load(jiaUserID); !ok {
		return "", http.StatusUnauthorized, fmt.Errorf("not found: user")
	}

	return jiaUserID, 0, nil
}

func getJIAServiceURL(tx *sqlx.Tx) string {
	var config Config
	err := tx.Get(&config, "SELECT * FROM `isu_association_config` WHERE `name` = ?", "jia_service_url")
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			log.Print(err)
		}
		return defaultJIAServiceURL
	}
	return config.URL
}

// POST /initialize
// サービスを初期化
func postInitialize(c echo.Context) error {
	var request InitializeRequest
	err := c.Bind(&request)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	takeConditions() // 前回の負荷走行の未書き込み分は捨てる

	cmd := exec.Command("../sql/init.sh")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	err = cmd.Run()
	if err != nil {
		c.Logger().Errorf("exec init.sh error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, err = db.Exec(
		"INSERT INTO `isu_association_config` (`name`, `url`) VALUES (?, ?) ON DUPLICATE KEY UPDATE `url` = VALUES(`url`)",
		"jia_service_url",
		request.JIAServiceURL,
	)
	if err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	takeConditions()
	if err := loadCache(); err != nil {
		c.Logger().Errorf("db error : %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	refreshTrend()

	return c.JSON(http.StatusOK, InitializeResponse{
		Language: "go",
	})
}

// POST /api/auth
// サインアップ・サインイン
func postAuthentication(c echo.Context) error {
	reqJwt := strings.TrimPrefix(c.Request().Header.Get("Authorization"), "Bearer ")

	token, err := jwt.Parse(reqJwt, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodECDSA); !ok {
			return nil, jwt.NewValidationError(fmt.Sprintf("unexpected signing method: %v", token.Header["alg"]), jwt.ValidationErrorSignatureInvalid)
		}
		return jiaJWTSigningKey, nil
	})
	if err != nil {
		switch err.(type) {
		case *jwt.ValidationError:
			return c.String(http.StatusForbidden, "forbidden")
		default:
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.Logger().Errorf("invalid JWT payload")
		return c.NoContent(http.StatusInternalServerError)
	}
	jiaUserIDVar, ok := claims["jia_user_id"]
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}
	jiaUserID, ok := jiaUserIDVar.(string)
	if !ok {
		return c.String(http.StatusBadRequest, "invalid JWT payload")
	}

	_, err = db.Exec("INSERT IGNORE INTO user (`jia_user_id`) VALUES (?)", jiaUserID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session, err := getSession(c.Request())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	userSet.Load().Store(jiaUserID, struct{}{})

	session.Values["jia_user_id"] = jiaUserID
	err = session.Save(c.Request(), c.Response())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.NoContent(http.StatusOK)
}

// POST /api/signout
// サインアウト
func postSignout(c echo.Context) error {
	_, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session, err := getSession(c.Request())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	session.Options = &sessions.Options{MaxAge: -1, Path: "/"}
	err = session.Save(c.Request(), c.Response())
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	return c.NoContent(http.StatusOK)
}

// GET /api/user/me
// サインインしている自分自身の情報を取得
func getMe(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	res := GetMeResponse{JIAUserID: jiaUserID}
	return c.JSON(http.StatusOK, res)
}

// GET /api/isu
// ISUの一覧を取得
func getIsuList(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	isuMu.RLock()
	isuList := isuByUser[jiaUserID]
	isuMu.RUnlock()

	responseList := make([]GetIsuListResponse, 0, len(isuList))
	for _, isu := range isuList {
		var formattedCondition *GetIsuConditionResponse
		if l := isu.latest.Load(); l != nil {
			formattedCondition = &GetIsuConditionResponse{
				JIAIsuUUID:     isu.UUID,
				IsuName:        isu.Name,
				Timestamp:      l.Timestamp,
				IsSitting:      l.IsSitting,
				Condition:      l.Condition,
				ConditionLevel: l.Level,
				Message:        l.Message,
			}
		}
		responseList = append(responseList, GetIsuListResponse{
			ID:                 isu.ID,
			JIAIsuUUID:         isu.UUID,
			Name:               isu.Name,
			Character:          isu.Character,
			LatestIsuCondition: formattedCondition})
	}

	return c.JSON(http.StatusOK, responseList)
}

// POST /api/isu
// ISUを登録
func postIsu(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	useDefaultImage := false

	jiaIsuUUID := c.FormValue("jia_isu_uuid")
	isuName := c.FormValue("isu_name")
	fh, err := c.FormFile("image")
	if err != nil {
		if !errors.Is(err, http.ErrMissingFile) {
			return c.String(http.StatusBadRequest, "bad format: icon")
		}
		useDefaultImage = true
	}

	var image []byte

	if useDefaultImage {
		image, err = ioutil.ReadFile(defaultIconFilePath)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	} else {
		file, err := fh.Open()
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
		defer file.Close()

		image, err = ioutil.ReadAll(file)
		if err != nil {
			c.Logger().Error(err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}

	tx, err := db.Beginx()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer tx.Rollback()

	_, err = tx.Exec("INSERT INTO `isu`"+
		"	(`jia_isu_uuid`, `name`, `image`, `jia_user_id`) VALUES (?, ?, ?, ?)",
		jiaIsuUUID, isuName, image, jiaUserID)
	if err != nil {
		mysqlErr, ok := err.(*mysql.MySQLError)

		if ok && mysqlErr.Number == uint16(mysqlErrNumDuplicateEntry) {
			return c.String(http.StatusConflict, "duplicated: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	targetURL := getJIAServiceURL(tx) + "/api/activate"
	body := JIAServiceRequest{postIsuConditionTargetBaseURL, jiaIsuUUID}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewBuffer(bodyJSON))
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	reqJIA.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(reqJIA)
	if err != nil {
		c.Logger().Errorf("failed to request to JIAService: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	resBody, err := ioutil.ReadAll(res.Body)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	if res.StatusCode != http.StatusAccepted {
		c.Logger().Errorf("JIAService returned error: status code %v, message: %v", res.StatusCode, string(resBody))
		return c.String(res.StatusCode, "JIAService returned error")
	}

	var isuFromJIA IsuFromJIA
	err = json.Unmarshal(resBody, &isuFromJIA)
	if err != nil {
		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	_, err = tx.Exec("UPDATE `isu` SET `character` = ? WHERE  `jia_isu_uuid` = ?", isuFromJIA.Character, jiaIsuUUID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	var isu Isu
	err = tx.Get(
		&isu,
		"SELECT `id`, `jia_isu_uuid`, `name`, `character` FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	err = tx.Commit()
	if err != nil {
		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}
	addIsu(&isuInfo{ID: isu.ID, UUID: isu.JIAIsuUUID, Name: isu.Name, Character: isu.Character, UserID: jiaUserID})

	return c.JSON(http.StatusCreated, isu)
}

// GET /api/isu/:jia_isu_uuid
// ISUの情報を取得
func getIsuID(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")

	isu := getIsu(jiaIsuUUID)
	if isu == nil || isu.UserID != jiaUserID {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	return c.JSON(http.StatusOK, Isu{ID: isu.ID, JIAIsuUUID: isu.UUID, Name: isu.Name, Character: isu.Character})
}

// GET /api/isu/:jia_isu_uuid/icon
// ISUのアイコンを取得
func getIsuIcon(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if isu := getIsu(jiaIsuUUID); isu == nil || isu.UserID != jiaUserID {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	var image []byte
	err = db.Get(&image, "SELECT `image` FROM `isu` WHERE `jia_user_id` = ? AND `jia_isu_uuid` = ?",
		jiaUserID, jiaIsuUUID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return c.String(http.StatusNotFound, "not found: isu")
		}

		c.Logger().Errorf("db error: %v", err)
		return c.NoContent(http.StatusInternalServerError)
	}

	// アイコンは登録後に変わらない。ユーザーごとのキャッシュに載せて再リクエストを無くす
	c.Response().Header().Set("Cache-Control", "private, max-age=86400")
	return c.Blob(http.StatusOK, "", image)
}

// GET /api/isu/:jia_isu_uuid/graph
// ISUのコンディショングラフ描画のための情報を取得
func getIsuGraph(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	datetimeStr := c.QueryParam("datetime")
	if datetimeStr == "" {
		return c.String(http.StatusBadRequest, "missing: datetime")
	}
	datetimeInt64, err := strconv.ParseInt(datetimeStr, 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: datetime")
	}
	date := datetimeInt64 - datetimeInt64%3600

	isu := getIsu(jiaIsuUUID)
	if isu == nil || isu.UserID != jiaUserID {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	res := generateIsuGraphResponse(isu, date)

	return c.JSON(http.StatusOK, res)
}

// グラフのデータ点を一日分生成（メモリ上のコンディションから。graphDate は 1 時間単位に切り捨てた unix 秒）
func generateIsuGraphResponse(isu *isuInfo, graphDate int64) []GraphResponse {
	responseList := make([]GraphResponse, 0, 24)

	isu.condMu.RLock()
	defer isu.condMu.RUnlock()
	conds := isu.conds
	idx := sort.Search(len(conds), func(k int) bool { return conds[k].ts >= graphDate })
	for h := int64(0); h < 24; h++ {
		start := graphDate + h*3600
		end := start + 3600
		from := idx
		for idx < len(conds) && conds[idx].ts < end {
			idx++
		}
		var data *GraphDataPoint
		timestamps := make([]int64, 0, idx-from)
		if idx > from {
			d := calculateGraphDataPoint(conds[from:idx])
			data = &d
			for _, c := range conds[from:idx] {
				timestamps = append(timestamps, c.ts)
			}
		}
		responseList = append(responseList, GraphResponse{
			StartAt:             start,
			EndAt:               end,
			Data:                data,
			ConditionTimestamps: timestamps,
		})
	}
	return responseList
}

// 複数のISUのコンディションからグラフの一つのデータ点を計算
func calculateGraphDataPoint(isuConditions []cond) GraphDataPoint {
	rawScore, sittingCount, broken, dirty, overweight := 0, 0, 0, 0, 0
	for _, c := range isuConditions {
		switch flagBadCount[c.flags] {
		case 3:
			rawScore += scoreConditionLevelCritical
		case 0:
			rawScore += scoreConditionLevelInfo
		default:
			rawScore += scoreConditionLevelWarning
		}
		if c.flags&flagBroken != 0 {
			broken++
		}
		if c.flags&flagDirty != 0 {
			dirty++
		}
		if c.flags&flagOverweight != 0 {
			overweight++
		}
		if c.sitting {
			sittingCount++
		}
	}
	n := len(isuConditions)
	return GraphDataPoint{
		Score: rawScore * 100 / 3 / n,
		Percentage: ConditionsPercentage{
			Sitting:      sittingCount * 100 / n,
			IsBroken:     broken * 100 / n,
			IsOverweight: overweight * 100 / n,
			IsDirty:      dirty * 100 / n,
		},
	}
}

// GET /api/condition/:jia_isu_uuid
// ISUのコンディションを取得
func getIsuConditions(c echo.Context) error {
	jiaUserID, errStatusCode, err := getUserIDFromSession(c)
	if err != nil {
		if errStatusCode == http.StatusUnauthorized {
			return c.String(http.StatusUnauthorized, "you are not signed in")
		}

		c.Logger().Error(err)
		return c.NoContent(http.StatusInternalServerError)
	}

	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	endTimeInt64, err := strconv.ParseInt(c.QueryParam("end_time"), 10, 64)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad format: end_time")
	}
	endTime := endTimeInt64
	conditionLevelCSV := c.QueryParam("condition_level")
	if conditionLevelCSV == "" {
		return c.String(http.StatusBadRequest, "missing: condition_level")
	}
	conditionLevel := map[string]interface{}{}
	for _, level := range strings.Split(conditionLevelCSV, ",") {
		conditionLevel[level] = struct{}{}
	}

	startTimeStr := c.QueryParam("start_time")
	startTime := int64(-1 << 62)
	if startTimeStr != "" {
		startTime, err = strconv.ParseInt(startTimeStr, 10, 64)
		if err != nil {
			return c.String(http.StatusBadRequest, "bad format: start_time")
		}
	}

	isu := getIsu(jiaIsuUUID)
	if isu == nil || isu.UserID != jiaUserID {
		return c.String(http.StatusNotFound, "not found: isu")
	}
	var levelOK [4]bool // "=true" の数ごとに返すかどうか
	_, levelOK[0] = conditionLevel[conditionLevelInfo]
	_, levelOK[1] = conditionLevel[conditionLevelWarning]
	levelOK[2] = levelOK[1]
	_, levelOK[3] = conditionLevel[conditionLevelCritical]

	// メモリ上のコンディションを新しい順に最大 conditionLimit 件
	conditionsResponse := make([]GetIsuConditionResponse, 0, conditionLimit)
	isu.condMu.RLock()
	conds := isu.conds
	for i := sort.Search(len(conds), func(k int) bool { return conds[k].ts >= endTime }) - 1; i >= 0 && len(conditionsResponse) < conditionLimit; i-- {
		cd := &conds[i]
		if cd.ts < startTime {
			break
		}
		if !levelOK[flagBadCount[cd.flags]] {
			continue
		}
		conditionsResponse = append(conditionsResponse, GetIsuConditionResponse{
			JIAIsuUUID:     isu.UUID,
			IsuName:        isu.Name,
			Timestamp:      cd.ts,
			IsSitting:      cd.sitting,
			Condition:      conditionStrings[cd.flags],
			ConditionLevel: flagLevels[cd.flags],
			Message:        cd.msg,
		})
	}
	isu.condMu.RUnlock()
	return c.JSON(http.StatusOK, conditionsResponse)
}

// ISUのコンディションの文字列からコンディションレベルを計算
func calculateConditionLevel(condition string) (string, error) {
	var conditionLevel string

	warnCount := strings.Count(condition, "=true")
	switch warnCount {
	case 0:
		conditionLevel = conditionLevelInfo
	case 1, 2:
		conditionLevel = conditionLevelWarning
	case 3:
		conditionLevel = conditionLevelCritical
	default:
		return "", fmt.Errorf("unexpected warn count")
	}

	return conditionLevel, nil
}

// GET /api/trend
// ISUの性格毎の最新のコンディション情報
func getTrend(c echo.Context) error {
	body, ok := trendCache.Load().([]byte)
	if !ok {
		var err error
		body, err = refreshTrend()
		if err != nil {
			c.Logger().Errorf("db error: %v", err)
			return c.NoContent(http.StatusInternalServerError)
		}
	}
	return c.JSONBlob(http.StatusOK, body)
}

// トレンドのレスポンス（全ユーザー共通）。trendLoop が定期的に作り直す
var trendCache atomic.Value

// 作り直す間隔。閲覧者がトレンドの変化を見る頻度（=ユーザーの増え方）に効くので環境変数で振れるようにする
var trendInterval = time.Duration(envInt("TREND_INTERVAL_MS", 500)) * time.Millisecond

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func trendLoop() {
	for {
		time.Sleep(trendInterval)
		if _, err := refreshTrend(); err != nil {
			log.Errorf("refreshTrend: %v", err)
		}
	}
}

func refreshTrend() ([]byte, error) {
	body, err := buildTrend()
	if err != nil {
		return nil, err
	}
	trendCache.Store(body)
	return body, nil
}

func buildTrend() ([]byte, error) {
	byCharacter := map[string]*TrendResponse{}
	res := []*TrendResponse{}

	isuMu.RLock()
	for _, isu := range isuByUUID {
		t, ok := byCharacter[isu.Character]
		if !ok {
			t = &TrendResponse{Character: isu.Character, Info: []*TrendCondition{}, Warning: []*TrendCondition{}, Critical: []*TrendCondition{}}
			byCharacter[isu.Character] = t
			res = append(res, t)
		}
		l := isu.latest.Load()
		if l == nil {
			continue
		}
		tc := &TrendCondition{ID: isu.ID, Timestamp: l.Timestamp}
		switch l.Level {
		case conditionLevelInfo:
			t.Info = append(t.Info, tc)
		case conditionLevelWarning:
			t.Warning = append(t.Warning, tc)
		case conditionLevelCritical:
			t.Critical = append(t.Critical, tc)
		}
	}
	isuMu.RUnlock()

	sort.Slice(res, func(i, j int) bool { return res[i].Character < res[j].Character })
	for _, t := range res {
		for _, l := range [][]*TrendCondition{t.Info, t.Warning, t.Critical} {
			l := l
			sort.Slice(l, func(i, j int) bool { return l[i].Timestamp > l[j].Timestamp })
		}
	}
	return json.Marshal(res)
}

// POST /api/condition/:jia_isu_uuid
// ISUからのコンディションを受け取る
func postIsuCondition(c echo.Context) error {
	jiaIsuUUID := c.Param("jia_isu_uuid")
	if jiaIsuUUID == "" {
		return c.String(http.StatusBadRequest, "missing: jia_isu_uuid")
	}

	req := []PostIsuConditionRequest{}
	err := c.Bind(&req)
	if err != nil {
		return c.String(http.StatusBadRequest, "bad request body")
	} else if len(req) == 0 {
		return c.String(http.StatusBadRequest, "bad request body")
	}

	isu := getIsu(jiaIsuUUID)
	if isu == nil {
		return c.String(http.StatusNotFound, "not found: isu")
	}

	rows := make([]condRow, 0, len(req))
	conds := make([]cond, 0, len(req))
	newest := 0
	for i := range req {
		r := &req[i]
		flags, ok := parseCondition(r.Condition)
		if !ok {
			return c.String(http.StatusBadRequest, "bad request body")
		}
		rows = append(rows, condRow{UUID: jiaIsuUUID, Timestamp: time.Unix(r.Timestamp, 0), IsSitting: r.IsSitting, Condition: conditionStrings[flags], Message: r.Message})
		conds = append(conds, cond{ts: r.Timestamp, sitting: r.IsSitting, flags: flags, msg: intern(r.Message)})
		if r.Timestamp > req[newest].Timestamp {
			newest = i
		}
	}
	isu.addConds(conds)     // 読み出し（一覧・グラフ）はメモリから。即時に見える
	enqueueConditions(rows) // DB へは非同期（永続化用）

	// 最新コンディション（一覧・トレンド用）を更新する
	n := conds[newest]
	nl := &latestCond{Timestamp: n.ts, IsSitting: n.sitting, Condition: conditionStrings[n.flags], Message: n.msg, Level: flagLevels[n.flags]}
	for {
		old := isu.latest.Load()
		if old != nil && old.Timestamp >= nl.Timestamp {
			break
		}
		if isu.latest.CompareAndSwap(old, nl) {
			break
		}
	}

	return c.NoContent(http.StatusAccepted)
}

// ISUのコンディションの文字列がcsv形式になっているか検証
func isValidConditionFormat(conditionStr string) bool {

	keys := []string{"is_dirty=", "is_overweight=", "is_broken="}
	const valueTrue = "true"
	const valueFalse = "false"

	idxCondStr := 0

	for idxKeys, key := range keys {
		if !strings.HasPrefix(conditionStr[idxCondStr:], key) {
			return false
		}
		idxCondStr += len(key)

		if strings.HasPrefix(conditionStr[idxCondStr:], valueTrue) {
			idxCondStr += len(valueTrue)
		} else if strings.HasPrefix(conditionStr[idxCondStr:], valueFalse) {
			idxCondStr += len(valueFalse)
		} else {
			return false
		}

		if idxKeys < (len(keys) - 1) {
			if conditionStr[idxCondStr] != ',' {
				return false
			}
			idxCondStr++
		}
	}

	return (idxCondStr == len(conditionStr))
}

func getIndex(c echo.Context) error {
	return c.File(frontendContentsPath + "/index.html")
}
