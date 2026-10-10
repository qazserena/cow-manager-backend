package portaluser

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"cow-manager-backend/internal/httpx"
)

// Detail 一个地址在官网 / 游戏 / 日志三个库里的全部画像。
// 每一节独立查询,某一节失败只记日志、置空,不影响其它节。
type Detail struct {
	Address string `json:"address"`

	Profile  *Profile        `json:"profile"`
	Invite   *InviteSummary  `json:"invite"`
	Social   []SocialAccount `json:"social"`
	Beta     *BetaSummary    `json:"beta"`
	Cattle   *CattleSummary  `json:"cattle"`
	Planet   *PlanetSummary  `json:"planet"`
	Market   *MarketSummary  `json:"market"`
	Breed    []BreedListing  `json:"breed"`
	Airdrops []AirdropReward `json:"airdrops"`
	Legacy   *LegacyReferral `json:"legacy"`
	IPs      []IPSeen        `json:"ips"`

	Game *GameSummary `json:"game"` // 游戏库不可用或未注册时为 nil
	Logs *LogSummary  `json:"logs"` // 日志库不可用时为 nil

	Errors []string `json:"errors,omitempty"` // 哪些节查询失败
}

// Profile u_profile
type Profile struct {
	Address   string `db:"address" json:"address"`
	Name      string `db:"name" json:"name"`
	Avatar    string `db:"avatar" json:"avatar"`
	AvatarURL string `json:"avatarUrl"`
	Gender    int    `db:"gender" json:"gender"`
	UpdateNum int    `db:"update_num" json:"updateNum"`
	UsedFree  bool   `db:"used_free" json:"usedFree"`
	UpdateAt  string `db:"update_at" json:"updateAt"`
}

// InviteSummary u_invite_user(+ 名次)
type InviteSummary struct {
	Code        string `db:"code" json:"code"`
	Link        string `json:"link"`
	Inviter     string `db:"inviter" json:"inviter"`
	BindIP      string `db:"bind_ip" json:"bindIp"`
	BindAt      string `db:"bind_at" json:"bindAt"`
	Flag        string `db:"flag" json:"flag"`
	Stage       int    `db:"stage" json:"stage"`
	Points      int64  `db:"points" json:"points"`
	PointsAt    string `db:"points_at" json:"pointsAt"`
	InviteTotal int64  `db:"invite_total" json:"inviteTotal"`
	InviteValid int64  `db:"invite_valid" json:"inviteValid"`
	CreatedAt   string `db:"created_at" json:"createdAt"`
	Level       int    `json:"level"`
	LevelKey    string `json:"levelKey"`
	Rank        int64  `json:"rank"`
	// 名下被邀请人:计分 / 进游戏 / 有效 / 被过滤
	Invitees struct {
		Total     int64 `db:"total" json:"total"`
		Activated int64 `db:"activated" json:"activated"`
		Valid     int64 `db:"valid" json:"valid"`
		Flagged   int64 `db:"flagged" json:"flagged"`
	} `json:"invitees"`
	PointsByKind []KindPoints `json:"pointsByKind"`
}

// KindPoints 按流水类型汇总
type KindPoints struct {
	Kind   string `db:"kind" json:"kind"`
	Count  int64  `db:"cnt" json:"count"`
	Points int64  `db:"pts" json:"points"`
}

// SocialAccount u_social_account(不返回 token)
type SocialAccount struct {
	Platform     string `db:"platform" json:"platform"`
	SocialID     string `db:"social_id" json:"socialId"`
	Handle       string `db:"handle" json:"handle"`
	Name         string `db:"name" json:"name"`
	Avatar       string `db:"avatar" json:"avatar"`
	Status       string `db:"status" json:"status"`
	Flag         string `db:"flag" json:"flag"`
	BindIP       string `db:"bind_ip" json:"bindIp"`
	BoundAt      string `db:"bound_at" json:"boundAt"`
	VerifiedAt   string `db:"verified_at" json:"verifiedAt"`
	Keep7At      string `db:"keep7_at" json:"keep7At"`
	Keep30At     string `db:"keep30_at" json:"keep30At"`
	RevokedAt    string `db:"revoked_at" json:"revokedAt"`
	LastCheckAt  string `db:"last_check_at" json:"lastCheckAt"`
	LastCheckErr string `db:"last_check_err" json:"lastCheckErr"`
}

// BetaSummary u_beta_claim_log
type BetaSummary struct {
	Starter int64     `json:"starter"` // kind=1 签发次数
	Daily   int64     `json:"daily"`   // kind=2 签发次数
	First   string    `json:"first"`
	Last    string    `json:"last"`
	Recent  []BetaLog `json:"recent"`
}

// BetaLog 一条签发记录
type BetaLog struct {
	Kind      int    `db:"kind" json:"kind"`
	IP        string `db:"ip" json:"ip"`
	CreatedAt string `db:"created_at" json:"createdAt"`
}

// CattleSummary u_cattle / r_burn_cattle / r_cattle_compound
type CattleSummary struct {
	Alive    int64    `json:"alive"`
	Dead     int64    `json:"dead"`
	Burned   int64    `json:"burned"`
	Compound int64    `json:"compound"`
	List     []Cattle `json:"list"`
}

// Cattle 一头牛(带模板名)
type Cattle struct {
	ID        int64   `db:"id" json:"id"`
	Class     int     `db:"class" json:"class"`
	IsAdult   bool    `db:"is_adult" json:"isAdult"`
	Gender    int     `db:"gender" json:"gender"`
	GenderSeq int     `db:"gender_seq" json:"genderSeq"`
	Star      int     `db:"star" json:"star"`
	Life      int64   `db:"life" json:"life"`
	Growth    int64   `db:"growth" json:"growth"`
	Energy    int64   `db:"energy" json:"energy"`
	Attack    int64   `db:"attack" json:"attack"`
	Stamina   int64   `db:"stamina" json:"stamina"`
	Defense   int64   `db:"defense" json:"defense"`
	Milk      int64   `db:"milk" json:"milk"`
	MilkRate  float64 `db:"milk_rate" json:"milkRate"`
	DeadAt    int64   `db:"dead_at" json:"deadAt"`
	Parents   string  `db:"parents" json:"parents"`
	Name      string  `db:"name" json:"name"`
	Image     string  `db:"image" json:"image"`
	ImageURL  string  `json:"imageUrl"`
	OnMarket  bool    `json:"onMarket"`
	OnBreed   bool    `json:"onBreed"`
	IsDead    bool    `json:"isDead"`
}

// PlanetSummary 星球 / 公会
type PlanetSummary struct {
	Member   *PlanetMember `json:"member"`   // 所在星球
	Mastered []Planet      `json:"mastered"` // 名下(会长)星球
}

// PlanetMember u_planet_member + u_planet
type PlanetMember struct {
	PlanetID   int64  `db:"planet_id" json:"planetId"`
	Position   int    `db:"position" json:"position"`
	Score      int64  `db:"score" json:"score"`
	JoinedAt   int64  `db:"create_at" json:"joinedAt"`
	PlanetName string `db:"name" json:"planetName"`
	PlanetType int    `db:"planet_type" json:"planetType"`
	Master     string `db:"master" json:"master"`
	Fee        int    `db:"fee" json:"fee"`
	Population int64  `db:"population" json:"population"`
}

// Planet u_planet
type Planet struct {
	ID         int64  `db:"id" json:"id"`
	PlanetType int    `db:"planet_type" json:"planetType"`
	Name       string `db:"name" json:"name"`
	Fee        int    `db:"fee" json:"fee"`
	ParentID   int64  `db:"parent_id" json:"parentId"`
	Population int64  `db:"population" json:"population"`
	TotalScore int64  `db:"total_score" json:"totalScore"`
	CreateAt   int64  `db:"create_at" json:"createAt"`
	Notice     string `db:"notice" json:"notice"`
}

// MarketSummary u_market / r_market
type MarketSummary struct {
	Listings []Listing `json:"listings"`
	Sold     int64     `json:"sold"`
	Bought   int64     `json:"bought"`
	Trades   []Trade   `json:"trades"`
}

// Listing u_market
type Listing struct {
	ID        int64   `db:"id" json:"id"`
	GoodsType int     `db:"goods_type" json:"goodsType"`
	GoodsName string  `db:"goods_name" json:"goodsName"`
	TokenID   int64   `db:"token_id" json:"tokenId"`
	TradeType int     `db:"trade_type" json:"tradeType"`
	Price     float64 `db:"price" json:"price"`
	UPrice    float64 `db:"u_price" json:"uPrice"`
	BlockTime int64   `db:"block_time" json:"blockTime"`
}

// Trade r_market
type Trade struct {
	ID        int64   `db:"id" json:"id"`
	GoodsType int     `db:"goods_type" json:"goodsType"`
	GoodsName string  `db:"goods_name" json:"goodsName"`
	TokenID   int64   `db:"token_id" json:"tokenId"`
	TradeType int     `db:"trade_type" json:"tradeType"`
	Price     float64 `db:"price" json:"price"`
	Seller    string  `db:"seller" json:"seller"`
	Buyer     string  `db:"buyer" json:"buyer"`
	BlockTime int64   `db:"block_time" json:"blockTime"`
}

// BreedListing u_breed_center
type BreedListing struct {
	ID           int64   `db:"id" json:"id"`
	TokenID      int64   `db:"token_id" json:"tokenId"`
	CurrencyType int     `db:"currency_type" json:"currencyType"`
	Price        float64 `db:"price" json:"price"`
	CreateAt     int64   `db:"create_at" json:"createAt"`
}

// AirdropReward u_airdrop_reward + s_airdrop_title
type AirdropReward struct {
	ID       int64  `db:"id" json:"id"`
	Type     int    `db:"type" json:"type"`
	TitleID  int64  `db:"title_id" json:"titleId"`
	Title    string `db:"title" json:"title"`
	Category int    `db:"category" json:"category"`
	Amount   string `db:"amount" json:"amount"`
	CardID   int64  `db:"card_id" json:"cardId"`
	Status   int    `db:"status" json:"status"`
}

// LegacyReferral 旧版链上推荐(r_invitation / r_invite_reward)
type LegacyReferral struct {
	Inviter       string `json:"inviter"`
	InvitedCount  int64  `json:"invitedCount"`
	RewardRecords int64  `json:"rewardRecords"`
}

// IPSeen 地址出现过的 IP(各来源合并)
type IPSeen struct {
	IP     string `json:"ip"`
	Source string `json:"source"`
	SeenAt string `json:"seenAt"`
}

// GameSummary ranch_game
type GameSummary struct {
	User struct {
		UID          int64  `db:"uid" json:"uid"`
		Account      string `db:"account" json:"account"`
		GuildID      int64  `db:"guildId" json:"guildId"`
		FedPlanetID  int64  `db:"fedPlanetId" json:"fedPlanetId"`
		LinkEnergy   int64  `db:"linkEnergy" json:"linkEnergy"`
		CostEnergy   int64  `db:"costEnergy" json:"costEnergy"`
		Motto        string `db:"motto" json:"motto"`
		HeadBoxID    int64  `db:"headBoxId" json:"headBoxId"`
		RegisterTs   int64  `db:"registerTs" json:"registerTs"`
		JoinGuildTs  int64  `db:"joinGuildTs" json:"joinGuildTs"`
		LastLoginIP  string `db:"lastLoginIP" json:"lastLoginIP"`
		LastLogoutTs int64  `db:"lastLogoutTs" json:"lastLogoutTs"`
	} `json:"user"`
	GuildOwner   bool              `json:"guildOwner"`
	GuildPoints  int64             `json:"guildPoints"`
	Tasks        TaskStats         `json:"tasks"`
	Friends      FriendStats       `json:"friends"`
	Mails        MailStats         `json:"mails"`
	Bag          []BagItem         `json:"bag"`
	Dict         map[string]any    `json:"dict"`
	Bullring     []BullringDay     `json:"bullring"`
	Ranks        []RankRow         `json:"ranks"`
	Rewards      []RewardEntry     `json:"rewards"`
	RewardOrders []RewardOrder     `json:"rewardOrders"`
	Notifies     int64             `json:"notifies"`
	Activity     map[string]string `json:"activity"`
}

// TaskStats ranch_task
type TaskStats struct {
	Total    int64 `db:"total" json:"total"`
	Finished int64 `db:"finished" json:"finished"`
	Drawn    int64 `db:"drawn" json:"drawn"`
}

// FriendStats ranch_friends(list* 为 JSON 数组)
type FriendStats struct {
	Friends   int `json:"friends"`
	List1     int `json:"list1"`
	List2     int `json:"list2"`
	Relations int `json:"relations"`
	Applies   int `json:"applies"`
}

// MailStats ranch_mail
type MailStats struct {
	Total   int64 `db:"total" json:"total"`
	Unread  int64 `db:"unread" json:"unread"`
	Undrawn int64 `db:"undrawn" json:"undrawn"`
}

// BagItem ranch_bag
type BagItem struct {
	ItemID   int64 `db:"itemId" json:"itemId"`
	TplID    int64 `db:"tplId" json:"tplId"`
	Stack    int64 `db:"stack" json:"stack"`
	ExpireTs int64 `db:"expireTs" json:"expireTs"`
	GainType int   `db:"gainType" json:"gainType"`
	GainTs   int64 `db:"gainTs" json:"gainTs"`
}

// BullringDay ranch_bullring
type BullringDay struct {
	Dt             string `db:"dt" json:"dt"`
	PveWins        int64  `db:"pveWins" json:"pveWins"`
	PvpWins        int64  `db:"pvpWins" json:"pvpWins"`
	MultiWins      int64  `db:"multiWins" json:"multiWins"`
	FiveVFiveWins  int64  `db:"fiveVFiveWins" json:"fiveVFiveWins"`
	AffinitiveWins int64  `db:"affinitiveWins" json:"affinitiveWins"`
}

// RankRow ranch_rank
type RankRow struct {
	SeasonDate string `db:"seasonDate" json:"seasonDate"`
	RankType   int    `db:"rankType" json:"rankType"`
	RankNo     int64  `db:"rankNo" json:"rankNo"`
	Score      int64  `db:"score" json:"score"`
}

// RewardEntry ranch_rewards_center
type RewardEntry struct {
	ID          int64  `db:"id" json:"id"`
	SourceType  int    `db:"sourceType" json:"sourceType"`
	Attachments string `db:"attachments" json:"attachments"`
	WithTax     bool   `db:"withTax" json:"withTax"`
	PutInTs     int64  `db:"putInTs" json:"putInTs"`
	DrawTs      int64  `db:"drawTs" json:"drawTs"`
}

// RewardOrder ranch_rewards_order
type RewardOrder struct {
	OrderID  string `db:"orderId" json:"orderId"`
	Type     int    `db:"type" json:"type"`
	Status   int    `db:"status" json:"status"`
	Data     string `db:"data" json:"data"`
	CreateTs int64  `db:"createTs" json:"createTs"`
	UpdateTs int64  `db:"updateTs" json:"updateTs"`
}

// LogSummary ranch_log
type LogSummary struct {
	Logins     []LoginLog `json:"logins"`
	LoginCount int64      `json:"loginCount"`
	Bullring   struct {
		Games int64 `db:"games" json:"games"`
		Wins  int64 `db:"wins" json:"wins"`
		Draws int64 `db:"draws" json:"draws"`
	} `json:"bullring"`
	RecentGames []BullringLog `json:"recentGames"`
	TaskRewards struct {
		Count  int64  `db:"cnt" json:"count"`
		TokenT string `db:"t" json:"tokenT"`
		TokenG string `db:"g" json:"tokenG"`
	} `json:"taskRewards"`
}

// LoginLog log_login
type LoginLog struct {
	LoginIP  string `db:"loginIP" json:"loginIP"`
	LoginTs  int64  `db:"loginTs" json:"loginTs"`
	LogoutTs int64  `db:"logoutTs" json:"logoutTs"`
}

// BullringLog log_bullring
type BullringLog struct {
	GameID    int64 `db:"gameID" json:"gameId"`
	CowID     int64 `db:"cowId" json:"cowId"`
	GameType  int   `db:"gameType" json:"gameType"`
	TurnsUsed int   `db:"turnsUsed" json:"turnsUsed"`
	IsWinning bool  `db:"isWinning" json:"isWinning"`
	IsDraw    bool  `db:"isDraw" json:"isDraw"`
	CreateTs  int64 `db:"createTs" json:"createTs"`
}

// ---------------------------------------------------------------------------

func (s *Service) detail(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	key := strings.TrimSpace(r.PathValue("address"))
	address := key
	if !isAddress(key) {
		// 允许用邀请码 / 游戏 UID 查
		var a string
		if len(key) == 12 {
			if err := s.portal.GetContext(ctx, &a, "SELECT address FROM u_invite_user WHERE code = ?", strings.ToUpper(key)); err == nil {
				address = a
			}
		} else if uid, err := parseInt(key); err == nil && s.game != nil {
			if err := s.game.GetContext(ctx, &a, "SELECT address FROM ranch_user WHERE uid = ?", uid); err == nil {
				address = a
			}
		}
		if !isAddress(address) {
			return httpx.NotFound("地址 / 邀请码 / UID 不存在")
		}
	}
	// 以官网资料里的大小写为准(checksum);没有资料时用传入值
	var canon string
	if err := s.portal.GetContext(ctx, &canon, "SELECT address FROM u_profile WHERE address = ? OR LOWER(address) = LOWER(?) LIMIT 1", address, address); err == nil {
		address = canon
	}

	d := &Detail{Address: address, Social: []SocialAccount{}, Breed: []BreedListing{}, Airdrops: []AirdropReward{}, IPs: []IPSeen{}}
	fail := func(section string, err error) {
		log.Printf("portaluser: %s of %s failed: %v", section, address, err)
		d.Errors = append(d.Errors, section)
	}
	var err error
	if d.Profile, err = s.loadProfile(ctx, address); err != nil {
		fail("profile", err)
	}
	if d.Invite, err = s.loadInvite(ctx, address); err != nil {
		fail("invite", err)
	}
	if d.Social, err = s.loadSocial(ctx, address); err != nil {
		fail("social", err)
	}
	if d.Beta, err = s.loadBeta(ctx, address); err != nil {
		fail("beta", err)
	}
	if d.Cattle, err = s.loadCattle(ctx, address); err != nil {
		fail("cattle", err)
	}
	if d.Planet, err = s.loadPlanet(ctx, address); err != nil {
		fail("planet", err)
	}
	if d.Market, err = s.loadMarket(ctx, address); err != nil {
		fail("market", err)
	}
	if d.Breed, err = s.loadBreed(ctx, address); err != nil {
		fail("breed", err)
	}
	if d.Airdrops, err = s.loadAirdrops(ctx, address); err != nil {
		fail("airdrops", err)
	}
	if d.Legacy, err = s.loadLegacy(ctx, address); err != nil {
		fail("legacy", err)
	}
	if s.game != nil {
		if d.Game, err = s.loadGame(ctx, address); err != nil {
			fail("game", err)
		}
	}
	if s.logdb != nil && d.Game != nil {
		if d.Logs, err = s.loadLogs(ctx, address, d.Game.User.UID); err != nil {
			fail("logs", err)
		}
	}
	d.IPs = s.collectIPs(ctx, address, d)
	httpx.OK(w, d)
	return nil
}

func parseInt(s string) (int64, error) {
	var n int64
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, errors.New("nan")
		}
		n = n*10 + int64(ch-'0')
	}
	if s == "" {
		return 0, errors.New("empty")
	}
	return n, nil
}

func noRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// ---------- 官网库 ----------

func (s *Service) loadProfile(ctx context.Context, address string) (*Profile, error) {
	p := &Profile{}
	err := s.portal.GetContext(ctx, p, `SELECT address, CASE WHEN name = address THEN '' ELSE name END AS name, avatar, gender, update_num, used_free,
		COALESCE(DATE_FORMAT(update_at, '%Y-%m-%d %H:%i:%s'), '') AS update_at FROM u_profile WHERE address = ?`, address)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.AvatarURL = s.avatarURL(ctx, p.Avatar)
	return p, nil
}

func (s *Service) loadInvite(ctx context.Context, address string) (*InviteSummary, error) {
	iv := &InviteSummary{}
	err := s.portal.GetContext(ctx, iv, `SELECT code, inviter, bind_ip, COALESCE(DATE_FORMAT(bind_at, '%Y-%m-%d %H:%i:%s'), '') AS bind_at, flag, stage, points,
		COALESCE(DATE_FORMAT(points_at, '%Y-%m-%d %H:%i:%s'), '') AS points_at, invite_total, invite_valid,
		DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at FROM u_invite_user WHERE address = ?`, address)
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	iv.Link = s.siteURL + "/betaInvite?ref=" + iv.Code
	lv := s.api.LevelOf(ctx, iv.Points)
	iv.Level, iv.LevelKey = lv.Level, lv.Key
	if iv.Points > 0 {
		var ahead int64
		_ = s.portal.GetContext(ctx, &ahead, `SELECT COUNT(*) FROM u_invite_user
			WHERE points > ? OR (points = ? AND (points_at < ? OR (points_at = ? AND address < ?)))`,
			iv.Points, iv.Points, iv.PointsAt, iv.PointsAt, address)
		iv.Rank = ahead + 1
	}
	if err := s.portal.GetContext(ctx, &iv.Invitees, `SELECT COALESCE(SUM(flag = ''), 0) AS total,
		COALESCE(SUM(flag = '' AND stage >= 1), 0) AS activated, COALESCE(SUM(flag = '' AND stage >= 2), 0) AS valid,
		COALESCE(SUM(flag <> ''), 0) AS flagged FROM u_invite_user WHERE inviter = ?`, address); err != nil {
		return iv, err
	}
	iv.PointsByKind = []KindPoints{}
	err = s.portal.SelectContext(ctx, &iv.PointsByKind, `SELECT kind, COUNT(*) AS cnt, COALESCE(SUM(points), 0) AS pts
		FROM u_invite_point_log WHERE address = ? GROUP BY kind ORDER BY pts DESC`, address)
	return iv, err
}

func (s *Service) loadSocial(ctx context.Context, address string) ([]SocialAccount, error) {
	out := []SocialAccount{}
	err := s.portal.SelectContext(ctx, &out, `SELECT platform, social_id, handle, name, avatar, status, flag, bind_ip,
		COALESCE(DATE_FORMAT(bound_at, '%Y-%m-%d %H:%i:%s'), '') AS bound_at,
		COALESCE(DATE_FORMAT(verified_at, '%Y-%m-%d %H:%i:%s'), '') AS verified_at,
		COALESCE(DATE_FORMAT(keep7_at, '%Y-%m-%d %H:%i:%s'), '') AS keep7_at,
		COALESCE(DATE_FORMAT(keep30_at, '%Y-%m-%d %H:%i:%s'), '') AS keep30_at,
		COALESCE(DATE_FORMAT(revoked_at, '%Y-%m-%d %H:%i:%s'), '') AS revoked_at,
		COALESCE(DATE_FORMAT(last_check_at, '%Y-%m-%d %H:%i:%s'), '') AS last_check_at, last_check_err
		FROM u_social_account WHERE address = ? ORDER BY platform`, address)
	return out, err
}

func (s *Service) loadBeta(ctx context.Context, address string) (*BetaSummary, error) {
	b := &BetaSummary{Recent: []BetaLog{}}
	row := struct {
		Starter int64  `db:"starter"`
		Daily   int64  `db:"daily"`
		First   string `db:"first"`
		Last    string `db:"last"`
	}{}
	if err := s.portal.GetContext(ctx, &row, `SELECT COALESCE(SUM(kind = 1), 0) AS starter, COALESCE(SUM(kind = 2), 0) AS daily,
		COALESCE(DATE_FORMAT(MIN(created_at), '%Y-%m-%d %H:%i:%s'), '') AS first, COALESCE(DATE_FORMAT(MAX(created_at), '%Y-%m-%d %H:%i:%s'), '') AS last
		FROM u_beta_claim_log WHERE address = ?`, address); err != nil {
		return nil, err
	}
	b.Starter, b.Daily, b.First, b.Last = row.Starter, row.Daily, row.First, row.Last
	err := s.portal.SelectContext(ctx, &b.Recent, `SELECT kind, ip, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') AS created_at
		FROM u_beta_claim_log WHERE address = ? ORDER BY id DESC LIMIT 30`, address)
	return b, err
}

func (s *Service) loadCattle(ctx context.Context, address string) (*CattleSummary, error) {
	c := &CattleSummary{List: []Cattle{}}
	if err := s.portal.SelectContext(ctx, &c.List, `SELECT u.id, u.class, u.is_adult, u.gender, u.gender_seq, u.star, u.life, u.growth, u.energy,
		u.attack, u.stamina, u.defense, u.milk, u.milk_rate, u.dead_at, COALESCE(u.parents, '') AS parents,
		COALESCE(t.name_cn, '') AS name, COALESCE(t.image, '') AS image
		FROM u_cattle u LEFT JOIN s_cattle t ON t.class = u.class AND t.gender = u.gender AND t.gsq = u.gender_seq AND t.is_adult = u.is_adult
		WHERE u.owner = ? ORDER BY u.id DESC LIMIT 300`, address); err != nil {
		return nil, err
	}
	nowSec := timeNow().Unix()
	onMarket, onBreed := map[int64]bool{}, map[int64]bool{}
	var ids []int64
	_ = s.portal.SelectContext(ctx, &ids, "SELECT token_id FROM u_market WHERE seller = ? AND goods_type = 1", address)
	for _, id := range ids {
		onMarket[id] = true
	}
	ids = nil
	_ = s.portal.SelectContext(ctx, &ids, "SELECT token_id FROM u_breed_center WHERE seller = ?", address)
	for _, id := range ids {
		onBreed[id] = true
	}
	for i := range c.List {
		x := &c.List[i]
		x.IsDead = x.DeadAt > 0 && x.DeadAt <= nowSec
		if x.Image != "" {
			x.ImageURL = s.imageHosting + x.Image
		}
		x.OnMarket, x.OnBreed = onMarket[x.ID], onBreed[x.ID]
		if x.IsDead {
			c.Dead++
		} else {
			c.Alive++
		}
	}
	if err := s.portal.GetContext(ctx, &c.Burned, "SELECT COUNT(*) FROM r_burn_cattle WHERE owner = ?", address); err != nil {
		return c, err
	}
	err := s.portal.GetContext(ctx, &c.Compound, "SELECT COUNT(*) FROM r_cattle_compound WHERE address = ?", address)
	return c, err
}

func (s *Service) loadPlanet(ctx context.Context, address string) (*PlanetSummary, error) {
	p := &PlanetSummary{Mastered: []Planet{}}
	m := &PlanetMember{}
	err := s.portal.GetContext(ctx, m, `SELECT pm.planet_id, pm.position, pm.score, pm.create_at,
		COALESCE(pl.name, '') AS name, COALESCE(pl.planet_type, 0) AS planet_type, COALESCE(pl.master, '') AS master,
		COALESCE(pl.fee, 0) AS fee, COALESCE(pl.population, 0) AS population
		FROM u_planet_member pm LEFT JOIN u_planet pl ON pl.id = pm.planet_id WHERE pm.player = ? ORDER BY pm.id DESC LIMIT 1`, address)
	if err == nil {
		p.Member = m
	} else if !noRows(err) {
		return nil, err
	}
	err = s.portal.SelectContext(ctx, &p.Mastered, `SELECT id, planet_type, name, fee, parent_id, population, total_score, create_at, COALESCE(notice, '') AS notice
		FROM u_planet WHERE master = ? ORDER BY id`, address)
	return p, err
}

func (s *Service) loadMarket(ctx context.Context, address string) (*MarketSummary, error) {
	m := &MarketSummary{Listings: []Listing{}, Trades: []Trade{}}
	if err := s.portal.SelectContext(ctx, &m.Listings, `SELECT u.id, u.goods_type, COALESCE(g.goods_name, '') AS goods_name, u.token_id, u.trade_type, u.price, u.u_price, u.block_time
		FROM u_market u LEFT JOIN s_market_goods g ON g.goods_type = u.goods_type WHERE u.seller = ? ORDER BY u.id DESC LIMIT 100`, address); err != nil {
		return nil, err
	}
	if err := s.portal.SelectContext(ctx, &m.Trades, `SELECT r.id, r.goods_type, COALESCE(g.goods_name, '') AS goods_name, r.token_id, r.trade_type, r.price, r.seller, r.buyer, r.block_time
		FROM r_market r LEFT JOIN s_market_goods g ON g.goods_type = r.goods_type WHERE r.seller = ? OR r.buyer = ? ORDER BY r.id DESC LIMIT 100`, address, address); err != nil {
		return nil, err
	}
	for _, t := range m.Trades {
		if strings.EqualFold(t.Seller, address) {
			m.Sold++
		}
		if strings.EqualFold(t.Buyer, address) {
			m.Bought++
		}
	}
	return m, nil
}

func (s *Service) loadBreed(ctx context.Context, address string) ([]BreedListing, error) {
	out := []BreedListing{}
	err := s.portal.SelectContext(ctx, &out, "SELECT id, token_id, currency_type, price, create_at FROM u_breed_center WHERE seller = ? ORDER BY id DESC LIMIT 100", address)
	return out, err
}

func (s *Service) loadAirdrops(ctx context.Context, address string) ([]AirdropReward, error) {
	out := []AirdropReward{}
	err := s.portal.SelectContext(ctx, &out, `SELECT a.id, a.type, a.title_id, COALESCE(t.title_en, '') AS title, a.category, COALESCE(a.amount, '') AS amount,
		COALESCE(a.card_id, 0) AS card_id, a.status FROM u_airdrop_reward a LEFT JOIN s_airdrop_title t ON t.id = a.title_id
		WHERE a.address = ? ORDER BY a.id DESC LIMIT 100`, address)
	return out, err
}

func (s *Service) loadLegacy(ctx context.Context, address string) (*LegacyReferral, error) {
	l := &LegacyReferral{}
	if err := s.portal.GetContext(ctx, &l.Inviter, "SELECT COALESCE(inviter, '') FROM r_invitation WHERE address = ? ORDER BY id DESC LIMIT 1", address); err != nil && !noRows(err) {
		return nil, err
	}
	if err := s.portal.GetContext(ctx, &l.InvitedCount, "SELECT COUNT(*) FROM r_invitation WHERE inviter = ?", address); err != nil {
		return nil, err
	}
	err := s.portal.GetContext(ctx, &l.RewardRecords, "SELECT COUNT(*) FROM r_invite_reward WHERE inviter = ? OR address = ?", address, address)
	return l, err
}

// ---------- 游戏库 ----------

func (s *Service) loadGame(ctx context.Context, address string) (*GameSummary, error) {
	g := &GameSummary{Bag: []BagItem{}, Dict: map[string]any{}, Bullring: []BullringDay{}, Ranks: []RankRow{}, Rewards: []RewardEntry{}, RewardOrders: []RewardOrder{}, Activity: map[string]string{}}
	err := s.game.GetContext(ctx, &g.User, `SELECT uid, COALESCE(account, '') AS account, guildId, fedPlanetId, linkEnergy, costEnergy, COALESCE(motto, '') AS motto,
		headBoxId, registerTs, joinGuildTs, COALESCE(lastLoginIP, '') AS lastLoginIP, lastLogoutTs FROM ranch_user WHERE address = ?`, strings.ToLower(address))
	if noRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	uid := g.User.UID
	if g.User.GuildID > 0 {
		var owner int64
		if err := s.game.GetContext(ctx, &owner, "SELECT ownerUid FROM ranch_guild WHERE guildId = ?", g.User.GuildID); err == nil {
			g.GuildOwner = owner == uid
		}
		_ = s.game.GetContext(ctx, &g.GuildPoints, "SELECT COALESCE(gvgPoints, 0) FROM ranch_guild WHERE guildId = ?", g.User.GuildID)
	}
	if err := s.game.GetContext(ctx, &g.Tasks, `SELECT COUNT(*) AS total, COALESCE(SUM(finishTs > 0 OR drawTs > 0), 0) AS finished, COALESCE(SUM(drawTs > 0), 0) AS drawn
		FROM ranch_task WHERE uid = ?`, uid); err != nil {
		return g, err
	}
	fr := struct {
		List, List1, List2, RelationList, RelationApplyList sql.NullString
	}{}
	if err := s.game.QueryRowxContext(ctx, "SELECT list, list1, list2, relationList, relationApplyList FROM ranch_friends WHERE uid = ?", uid).
		Scan(&fr.List, &fr.List1, &fr.List2, &fr.RelationList, &fr.RelationApplyList); err == nil {
		g.Friends = FriendStats{Friends: jsonLen(fr.List.String), List1: jsonLen(fr.List1.String), List2: jsonLen(fr.List2.String),
			Relations: jsonLen(fr.RelationList.String), Applies: jsonLen(fr.RelationApplyList.String)}
	}
	_ = s.game.GetContext(ctx, &g.Mails, `SELECT COUNT(*) AS total, COALESCE(SUM(readTs = 0), 0) AS unread,
		COALESCE(SUM(attachments IS NOT NULL AND attachments <> '' AND attachments <> '[]' AND drawTs = 0), 0) AS undrawn
		FROM ranch_mail WHERE uid = ? AND deleteTs = 0`, uid)
	_ = s.game.SelectContext(ctx, &g.Bag, "SELECT itemId, tplId, stack, expireTs, gainType, gainTs FROM ranch_bag WHERE uid = ? ORDER BY gainTs DESC LIMIT 200", uid)
	var dict []struct {
		K string `db:"k"`
		V string `db:"v"`
	}
	if err := s.game.SelectContext(ctx, &dict, "SELECT k, COALESCE(v, '') AS v FROM ranch_user_dict WHERE uid = ?", uid); err == nil {
		for _, kv := range dict {
			var any_ any
			if json.Unmarshal([]byte(kv.V), &any_) == nil {
				g.Dict[kv.K] = any_
			} else {
				g.Dict[kv.K] = kv.V
			}
		}
	}
	_ = s.game.SelectContext(ctx, &g.Bullring, "SELECT dt, pveWins, pvpWins, multiWins, fiveVFiveWins, affinitiveWins FROM ranch_bullring WHERE uid = ? ORDER BY id DESC LIMIT 60", uid)
	_ = s.game.SelectContext(ctx, &g.Ranks, "SELECT seasonDate, rankType, rankNo, score FROM ranch_rank WHERE uid = ? ORDER BY seasonDate DESC, rankType", uid)
	_ = s.game.SelectContext(ctx, &g.Rewards, "SELECT id, sourceType, COALESCE(attachments, '') AS attachments, withTax, putInTs, drawTs FROM ranch_rewards_center WHERE uid = ? ORDER BY id DESC LIMIT 50", uid)
	_ = s.game.SelectContext(ctx, &g.RewardOrders, "SELECT orderId, type, status, COALESCE(data, '') AS data, createTs, updateTs FROM ranch_rewards_order WHERE uid = ? ORDER BY createTs DESC LIMIT 50", uid)
	_ = s.game.GetContext(ctx, &g.Notifies, "SELECT COUNT(*) FROM ranch_notify WHERE uid = ? AND drawTime = 0", uid)
	var acts []struct {
		ActivityID string `db:"activityId"`
		Userdata   string `db:"userdata"`
	}
	if err := s.game.SelectContext(ctx, &acts, "SELECT CAST(activityId AS CHAR) AS activityId, COALESCE(userdata, '') AS userdata FROM ranch_activity WHERE uid = ?", uid); err == nil {
		for _, a := range acts {
			g.Activity[a.ActivityID] = a.Userdata
		}
	}
	return g, nil
}

func jsonLen(s string) int {
	if strings.TrimSpace(s) == "" {
		return 0
	}
	var arr []json.RawMessage
	if json.Unmarshal([]byte(s), &arr) == nil {
		return len(arr)
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(s), &obj) == nil {
		return len(obj)
	}
	return 0
}

// ---------- 日志库 ----------

func (s *Service) loadLogs(ctx context.Context, address string, uid int64) (*LogSummary, error) {
	l := &LogSummary{Logins: []LoginLog{}, RecentGames: []BullringLog{}}
	if err := s.logdb.SelectContext(ctx, &l.Logins, "SELECT COALESCE(loginIP, '') AS loginIP, loginTs, logoutTs FROM log_login WHERE uid = ? ORDER BY id DESC LIMIT 30", uid); err != nil {
		return nil, err
	}
	_ = s.logdb.GetContext(ctx, &l.LoginCount, "SELECT COUNT(*) FROM log_login WHERE uid = ?", uid)
	_ = s.logdb.GetContext(ctx, &l.Bullring, "SELECT COUNT(*) AS games, COALESCE(SUM(isWinning), 0) AS wins, COALESCE(SUM(isDraw), 0) AS draws FROM log_bullring WHERE uid = ?", uid)
	_ = s.logdb.SelectContext(ctx, &l.RecentGames, "SELECT gameID, cowId, gameType, turnsUsed, isWinning, isDraw, createTs FROM log_bullring WHERE uid = ? ORDER BY id DESC LIMIT 30", uid)
	_ = s.logdb.GetContext(ctx, &l.TaskRewards, "SELECT COUNT(*) AS cnt, CAST(COALESCE(SUM(tokenT), 0) AS CHAR) AS t, CAST(COALESCE(SUM(tokenG), 0) AS CHAR) AS g FROM log_task_bvx WHERE uid = ?", uid)
	_ = address
	return l, nil
}

// collectIPs 把各来源的 IP 汇总去重(同 IP 保留最近一次出现)。
func (s *Service) collectIPs(ctx context.Context, address string, d *Detail) []IPSeen {
	seen := map[string]IPSeen{}
	add := func(ip, source, at string) {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			return
		}
		if prev, ok := seen[ip]; !ok || at > prev.SeenAt {
			seen[ip] = IPSeen{IP: ip, Source: source, SeenAt: at}
		}
	}
	var rows []struct {
		IP     string `db:"ip"`
		SeenAt string `db:"seen_at"`
	}
	if err := s.portal.SelectContext(ctx, &rows, "SELECT ip, DATE_FORMAT(seen_at, '%Y-%m-%d %H:%i:%s') AS seen_at FROM u_invite_ip_log WHERE address = ?", address); err == nil {
		for _, r := range rows {
			add(r.IP, "官网访问", r.SeenAt)
		}
	}
	if d.Invite != nil {
		add(d.Invite.BindIP, "邀请绑定", d.Invite.BindAt)
	}
	if d.Beta != nil {
		for _, b := range d.Beta.Recent {
			add(b.IP, "公测领取", b.CreatedAt)
		}
	}
	for _, sa := range d.Social {
		add(sa.BindIP, "社交绑定("+sa.Platform+")", sa.BoundAt)
	}
	if d.Game != nil {
		add(d.Game.User.LastLoginIP, "游戏最后登录", fmtTs(d.Game.User.LastLogoutTs))
	}
	if d.Logs != nil {
		for _, lg := range d.Logs.Logins {
			add(lg.LoginIP, "游戏登录", fmtTs(lg.LoginTs))
		}
	}
	out := make([]IPSeen, 0, len(seen))
	for _, v := range seen {
		out = append(out, v)
	}
	sortIPs(out)
	return out
}
