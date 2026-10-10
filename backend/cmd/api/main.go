package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"lending-app/backend/internal/api"
	"lending-app/backend/internal/config"
	"lending-app/backend/internal/handlers"
	"lending-app/backend/internal/identity"
	"lending-app/backend/internal/infrastructure/logger"
	"lending-app/backend/internal/middleware"
	"lending-app/backend/internal/repositories"
	"lending-app/backend/internal/services"
	"lending-app/backend/pkg/auth"
	"lending-app/backend/pkg/crypto"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/redis/go-redis/v9"

	_ "github.com/lib/pq"
)

type App struct {
	db     *sql.DB
	redis  *redis.Client
	logger *logger.Logger
	router *http.ServeMux
	server *http.Server

	encryptor   *crypto.EncryptionService
	authService *services.AuthService
	policy      config.Config
}

type Config struct {
	Env         string
	Port        string
	DBHost      string
	DBPort      string
	DBUser      string
	DBPassword  string
	DBName      string
	DBSSLMode   string
	RedisAddr   string
	RedisPass   string
	JWTSecret   string
	JWTPrevSecret string
	TrustProxy  bool
	MigrateLock bool
}

func loadConfig() (*Config, error) {
	required := []string{"ENCRYPTION_KEY", "DB_HOST", "DB_USER", "DB_PASSWORD", "DB_NAME", "JWT_SECRET"}
	var missing []string
	for _, k := range required {
		if os.Getenv(k) == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing env vars: %v", missing)
	}
	if len(os.Getenv("JWT_SECRET")) < 32 {
		return nil, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}
	if prev := os.Getenv("JWT_SECRET_PREVIOUS"); prev != "" && len(prev) < 32 {
		return nil, fmt.Errorf("JWT_SECRET_PREVIOUS must be at least 32 characters")
	}
	env := strings.ToLower(os.Getenv("ENV"))
	if env == "" {
		env = "development"
	}
	ssl := os.Getenv("DB_SSLMODE")
	if ssl == "" {
		ssl = "disable"
	}
	if env == "production" && ssl == "disable" {
		return nil, fmt.Errorf("DB_SSLMODE=disable is forbidden in production")
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	dbPort := os.Getenv("DB_PORT")
	if dbPort == "" {
		dbPort = "5432"
	}
	return &Config{
		Env: env, Port: port,
		DBHost: os.Getenv("DB_HOST"), DBPort: dbPort,
		DBUser: os.Getenv("DB_USER"), DBPassword: os.Getenv("DB_PASSWORD"),
		DBName: os.Getenv("DB_NAME"), DBSSLMode: ssl,
		RedisAddr: firstNonEmpty(os.Getenv("REDIS_ADDR"), "localhost:6379"),
		RedisPass: os.Getenv("REDIS_PASSWORD"),
		JWTSecret: os.Getenv("JWT_SECRET"),
		JWTPrevSecret: os.Getenv("JWT_SECRET_PREVIOUS"),
		TrustProxy: strings.ToLower(os.Getenv("TRUST_PROXY")) == "true",
		MigrateLock: true,
	}, nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func main() {
	reencrypt := flag.Bool("reencrypt", false, "re-encrypt all PII to the primary key version and exit")
	dryRun := flag.Bool("dry-run", false, "with -reencrypt: report without writing")
	forceHMAC := flag.Bool("rekey-hmac", false, "with -reencrypt: rewrite all rows even if already at primary version (HMAC-only rotation)")
	flag.Parse()

	app := &App{}
	app.logger = logger.NewLogger()

	cfg, err := loadConfig()
	if err != nil {
		app.logger.Fatal("env validation", "error", err)
	}

	// Money policy from config.yaml (validated, fail fast). Secrets stay in env.
	// Empty path = CONFIG_PATH, else backend/config.yaml (run from repo root;
	// the Docker image sets CONFIG_PATH=/srv/config.yaml). A missing file
	// fails the boot — policy must never silently default with real money.
	policy, policySource, err := config.Load("")
	if err != nil {
		app.logger.Fatal("config", "error", err)
	}
	app.policy = policy
	app.logger.Info("policy loaded",
		"source", policySource,
		"bands", len(policy.Pricing.Bands),
		"lateFeeRate", policy.Fees.LateFeeRate.String(),
		"lateFeeCap", policy.Fees.LateFeeCap.String())

	enc, err := crypto.GetEncryptionService()
	if err != nil {
		app.logger.Fatal("encryption init", "error", err)
	}
	if err := enc.HealthCheck(); err != nil {
		app.logger.Fatal("encryption health", "error", err)
	}
	app.encryptor = enc

	if err := app.initDB(cfg); err != nil {
		app.logger.Fatal("db init", "error", err)
	}
	defer app.db.Close()

	if *reencrypt {
		if !enc.HasPrevious() && !*forceHMAC {
			app.logger.Info("rekey: single key configured and no -rekey-hmac; nothing to do")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		res, err := runRekey(ctx, app, enc, *dryRun, *forceHMAC)
		if err != nil {
			app.logger.Fatal("rekey", "error", err)
		}
		app.logger.Info("rekey complete",
			"dryRun", *dryRun,
			"scanned", res.scanned, "rewritten", res.rewritten,
			"skipped", res.skipped, "failed", res.failed)
		if res.failed > 0 {
			os.Exit(1)
		}
		return
	}

	if err := app.runMigrations(cfg); err != nil {
		app.logger.Fatal("migrations", "error", err)
	}

	if err := app.initRedis(cfg); err != nil {
		app.logger.Fatal("redis init", "error", err)
	}
	defer app.redis.Close()

	app.setupRoutes(cfg)
	app.startServer(cfg)
}

func (app *App) initDB(cfg *Config) error {
	connStr := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s connect_timeout=5 statement_timeout=8000",
		cfg.DBHost, cfg.DBPort, cfg.DBUser,
		cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
	)
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return err
	}
	// Pool sized for a small API; fail fast on saturation instead of
	// queueing forever (failover: surfacing errors beats wedging).
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Retry initial connect across DB restarts/failover (up to ~30s).
	var pingErr error
	for i := 0; i < 6; i++ {
		if pingErr = db.PingContext(ctx); pingErr == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("db ping: %w", pingErr)
		case <-time.After(time.Duration(i+1) * time.Second):
		}
	}
	if pingErr != nil {
		return fmt.Errorf("db ping: %w", pingErr)
	}
	app.db = db
	app.logger.Info("database connected")
	return nil
}

func (app *App) runMigrations(_ *Config) error {
	// Concurrency: exactly one replica migrates; others skip via advisory lock.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	got, unlock, err := repositories.TryAdvisoryLock(ctx, app.db, repositories.LockMigrations)
	if err != nil {
		return err
	}
	if !got {
		app.logger.Info("migrations skipped: lock held by another instance")
		return nil
	}
	defer unlock()
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	dir, err := resolveMigrationsDir()
	if err != nil {
		return err
	}
	if err := goose.Up(app.db, dir); err != nil {
		return err
	}
	app.logger.Info("migrations applied", "dir", dir)
	return nil
}

// resolveMigrationsDir finds the SQL migration directory regardless of the
// process working directory (systemd unit, container WORKDIR, ad-hoc run).
// Override explicitly with MIGRATIONS_DIR.
func resolveMigrationsDir() (string, error) {
	if d := strings.TrimSpace(os.Getenv("MIGRATIONS_DIR")); d != "" {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, nil
		}
		return "", fmt.Errorf("MIGRATIONS_DIR=%q is not a directory", d)
	}
	candidates := []string{"migrations", "backend/migrations"}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, exeDirJoin(exe, "migrations"))
	}
	for _, d := range candidates {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return d, nil
		}
	}
	return "", fmt.Errorf("migrations directory not found (tried %v); set MIGRATIONS_DIR", candidates)
}

func (app *App) initRedis(cfg *Config) error {
	app.redis = redis.NewClient(&redis.Options{
		Addr:            cfg.RedisAddr,
		Password:        cfg.RedisPass,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        20,
		MinIdleConns:    2,
		MaxRetries:      3,
		MinRetryBackoff: 50 * time.Millisecond,
		MaxRetryBackoff: 2 * time.Second,
		// Failover: rate limiting degrades fail-open (see middleware);
		// sessions/refresh tokens are in Postgres, so Redis loss never
		// locks users out — only refresh rotation needs DB anyway.
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	for i := 0; i < 4; i++ {
		if err = app.redis.Ping(ctx).Err(); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis ping: %w", err)
		case <-time.After(time.Duration(i+1) * time.Second):
		}
	}
	if err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}
	app.logger.Info("redis connected")
	return nil
}

func (app *App) setupRoutes(cfg *Config) {
	// Repositories
	userRepo := repositories.NewUserRepository(app.db, app.encryptor)
	loanRepo := repositories.NewLoanRepository(app.db)
	creditRepo := repositories.NewCreditRepository(app.db)
	repaymentRepo := repositories.NewRepaymentRepository(app.db)
	collectionRepo := repositories.NewCollectionRepository(app.db)
	auditRepo := repositories.NewAuditRepository(app.db)
	refreshRepo := repositories.NewRefreshTokenRepository(app.db)
	resetRepo := repositories.NewPasswordResetRepository(app.db)
	verificationRepo := repositories.NewVerificationRepository(app.db)
	kycRepo := repositories.NewKYCRepository(app.db)

	// Services
	jwtSvc := auth.NewJWTService(cfg.JWTSecret)
	if cfg.JWTPrevSecret != "" {
		jwtSvc = auth.NewJWTServiceWithPrevious(cfg.JWTSecret, cfg.JWTPrevSecret)
	}
	app.authService = services.NewAuthService(userRepo, refreshRepo, resetRepo, jwtSvc)
	verificationSvc := services.NewVerificationService(verificationRepo, userRepo, app.encryptor)
	// Identity provider: fake (deterministic) for dev/test. Production swaps
	// in a licensed vendor implementing identity.Provider — no other change.
	kycSvc := services.NewKYCService(kycRepo, userRepo, identity.FakeProvider{}, app.encryptor)
	loanSvc := services.NewLoanService(app.db, loanRepo, creditRepo, repaymentRepo, userRepo, app.policy.Pricing)
	creditSvc := services.NewCreditService(creditRepo, userRepo, loanRepo, repaymentRepo)
	repaymentSvc := services.NewRepaymentService(app.db, repaymentRepo, loanRepo, app.policy.Fees)
	collectionSvc := services.NewCollectionService(collectionRepo, loanRepo, repaymentRepo)
	collectionSvc.WithDB(app.db)
	collectionSvc.WithRepaymentSvc(repaymentSvc)
	adminSvc := services.NewAdminService(loanRepo, userRepo, collectionRepo, kycRepo)
	auditSvc := services.NewAuditService(auditRepo)

	// Handlers
	authH := handlers.NewAuthHandler(app.authService, verificationSvc, auditSvc, app.encryptor)
	loanH := handlers.NewLoanHandler(loanSvc, auditSvc)
	creditH := handlers.NewCreditHandler(creditSvc, auditSvc)
	repaymentH := handlers.NewRepaymentHandler(repaymentSvc, auditSvc)
	collectionH := handlers.NewCollectionHandler(collectionSvc, auditSvc)
	adminH := handlers.NewAdminHandler(adminSvc, auditSvc)
	healthH := handlers.NewHealthHandler(app.encryptor, app.db, app.redis)
	kycH := handlers.NewKYCHandler(kycSvc, auditSvc)

	app.router = api.NewRouter(
		api.Deps{
			Auth: authH, Loan: loanH, Credit: creditH,
			Repayment: repaymentH, Collection: collectionH,
			Admin: adminH, Health: healthH, KYC: kycH,
		},
		api.MW{
			Logger:      middleware.NewLoggerMiddleware(app.logger),
			Security:    middleware.NewSecurityMiddleware(),
			RateLimiter: middleware.NewRateLimiter(app.redis),
			Auth:        middleware.NewAuthMiddleware(app.authService),
			Admin:       middleware.NewAdminMiddleware(),
		},
		cfg.TrustProxy,
		// Raw tokens/OTPs echo only in dev/test — never staging/demo/prod,
		// which are network-reachable and may hold realistic data.
		cfg.Env == "development" || cfg.Env == "test",
	)
}

func exeDirJoin(exe, sub string) string {
	return filepath.Join(filepath.Dir(exe), sub)
}

func (app *App) startServer(cfg *Config) {
	// Timeouts: slowloris-safe, failover-friendly (LB retries need bounded latency).
	readHeader, _ := strconv.Atoi(firstNonEmpty(os.Getenv("READ_HEADER_TIMEOUT_S"), "10"))
	if readHeader <= 0 {
		readHeader = 10
	}
	app.server = &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.router,
		ReadTimeout:       15 * time.Second,
		ReadHeaderTimeout: time.Duration(readHeader) * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	go func() {
		app.logger.Info("server starting", "port", cfg.Port, "env", cfg.Env)
		if err := app.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			app.logger.Fatal("server", "error", err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	app.logger.Info("shutdown signal received")
	// Graceful drain: stop accepting, finish in-flight (incl. txns) up to 30s.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.server.Shutdown(ctx); err != nil {
		app.logger.Error("shutdown", "error", err)
	}
	app.logger.Info("server stopped")
}
