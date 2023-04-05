package sqldb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"time"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"upper.io/db.v3/lib/sqlbuilder"

	"upper.io/db.v3/mysql"
	"upper.io/db.v3/postgresql"

	"github.com/argoproj/argo-workflows/v3/config"
	"github.com/argoproj/argo-workflows/v3/errors"
	"github.com/argoproj/argo-workflows/v3/util"

	mysqldriver "github.com/go-sql-driver/mysql"
)

func GetTableName(persistConfig *config.PersistConfig) (string, error) {
	var tableName string
	if persistConfig.PostgreSQL != nil {
		tableName = persistConfig.PostgreSQL.TableName

	} else if persistConfig.MySQL != nil {
		tableName = persistConfig.MySQL.TableName
	}
	if tableName == "" {
		return "", errors.InternalError("TableName is empty")
	} else {
		return tableName, nil
	}
}

// CreateDBSession creates the dB session
func CreateDBSession(kubectlConfig kubernetes.Interface, namespace string, persistConfig *config.PersistConfig) (sqlbuilder.Database, error) {
	if persistConfig == nil {
		return nil, errors.InternalError("Persistence config is not found")
	}

	if persistConfig.PostgreSQL != nil {
		return CreatePostGresDBSession(kubectlConfig, namespace, persistConfig.PostgreSQL, persistConfig.ConnectionPool)
	} else if persistConfig.MySQL != nil {
		return CreateMySQLDBSession(kubectlConfig, namespace, persistConfig.MySQL, persistConfig.ConnectionPool)
	}
	return nil, fmt.Errorf("no databases are configured")
}

// CreatePostGresDBSession creates postgresDB session
func CreatePostGresDBSession(kubectlConfig kubernetes.Interface, namespace string, cfg *config.PostgreSQLConfig, persistPool *config.ConnectionPool) (sqlbuilder.Database, error) {
	ctx := context.Background()
	userNameByte, err := util.GetSecrets(ctx, kubectlConfig, namespace, cfg.UsernameSecret.Name, cfg.UsernameSecret.Key)
	if err != nil {
		return nil, err
	}
	passwordByte, err := util.GetSecrets(ctx, kubectlConfig, namespace, cfg.PasswordSecret.Name, cfg.PasswordSecret.Key)
	if err != nil {
		return nil, err
	}

	settings := postgresql.ConnectionURL{
		User:     string(userNameByte),
		Password: string(passwordByte),
		Host:     cfg.GetHostname(),
		Database: cfg.Database,
	}

	if cfg.SSL {
		if cfg.SSLMode != "" {
			options := map[string]string{
				"sslmode": cfg.SSLMode,
			}
			settings.Options = options
		}
	}

	session, err := postgresql.Open(settings)
	if err != nil {
		return nil, err
	}
	session = ConfigureDBSession(session, persistPool)
	return session, nil
}

// CreateMySQLDBSession creates Mysql DB session
func CreateMySQLDBSession(kubectlConfig kubernetes.Interface, namespace string, cfg *config.MySQLConfig, persistPool *config.ConnectionPool) (sqlbuilder.Database, error) {
	if cfg.TableName == "" {
		return nil, errors.InternalError("tableName is empty")
	}

	ctx := context.Background()
	userNameByte, err := util.GetSecrets(ctx, kubectlConfig, namespace, cfg.UsernameSecret.Name, cfg.UsernameSecret.Key)
	if err != nil {
		return nil, err
	}
	passwordByte, err := util.GetSecrets(ctx, kubectlConfig, namespace, cfg.PasswordSecret.Name, cfg.PasswordSecret.Key)
	if err != nil {
		return nil, err
	}

	settings := mysql.ConnectionURL{
		User:     string(userNameByte),
		Password: string(passwordByte),
		Host:     cfg.GetHostname(),
		Database: cfg.Database,
	}

	if cfg.Options != nil {
		settings.Options = cfg.Options
	} else {
		settings.Options = map[string]string{}
	}

	if cfg.CaCertSecret != (apiv1.SecretKeySelector{}) {
		caCertByte, err := util.GetSecrets(ctx, kubectlConfig, namespace, cfg.CaCertSecret.Name, cfg.CaCertSecret.Key)
		if err != nil {
			return nil, "", err
		}

		rootCertPool := x509.NewCertPool()

		if ok := rootCertPool.AppendCertsFromPEM(caCertByte); !ok {
			return nil, "", fmt.Errorf("failed to append PEM")
		}

		err = mysqldriver.RegisterTLSConfig("argo-ca-cert", &tls.Config{
			RootCAs: rootCertPool,
		})
		if err != nil {
			return nil, "", err
		}

		settings.Options["tls"] = "argo-ca-cert"
	}

	session, err := mysql.Open(settings)
	if err != nil {
		return nil, err
	}
	session = ConfigureDBSession(session, persistPool)
	// this is needed to make MySQL run in a Golang-compatible UTF-8 character set.
	_, err = session.Exec("SET NAMES 'utf8mb4'")
	if err != nil {
		return nil, err
	}
	_, err = session.Exec("SET CHARACTER SET utf8mb4")
	if err != nil {
		return nil, err
	}
	return session, nil
}

// ConfigureDBSession configures the DB session
func ConfigureDBSession(session sqlbuilder.Database, persistPool *config.ConnectionPool) sqlbuilder.Database {
	if persistPool != nil {
		session.SetMaxOpenConns(persistPool.MaxOpenConns)
		session.SetMaxIdleConns(persistPool.MaxIdleConns)
		session.SetConnMaxLifetime(time.Duration(persistPool.ConnMaxLifetime))
	}
	return session
}
