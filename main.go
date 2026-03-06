package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/olivere/elastic/v7"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// 配置结构体
type Config struct {
	Database struct {
		Host         string `yaml:"host"`
		Port         int    `yaml:"port"`
		User         string `yaml:"user"`
		Password     string `yaml:"password"`
		DBName       string `yaml:"dbname"`
		Charset      string `yaml:"charset"`
		ParseTime    bool   `yaml:"parseTime"`
		Loc          string `yaml:"loc"`
		Timeout      string `yaml:"timeout"`
		ReadTimeout  string `yaml:"readTimeout"`
		WriteTimeout string `yaml:"writeTimeout"`
	} `yaml:"database"`

	Elasticsearch struct {
		URL   string `yaml:"url"`
		Sniff bool   `yaml:"sniff"`
	} `yaml:"elasticsearch"`

	Server struct {
		Port int    `yaml:"port"`
		Mode string `yaml:"mode"`
	} `yaml:"server"`

	Index struct {
		Manuscripts string `yaml:"manuscripts"`
		SyncInfo    string `yaml:"syncInfo"`
	} `yaml:"index"`

	Table struct {
		Manuscript string `yaml:"manuscript"`
		Journal    string `yaml:"journal"`
	} `yaml:"table"`
}

// 数据库模型
type Manuscript struct {
	Number      interface{} `json:"number" gorm:"column:number"`
	Title       string      `json:"title" gorm:"column:title"`
	Description string      `json:"description" gorm:"column:description"`
	JournalID   int         `json:"journal_id" gorm:"column:journal_id"`
	JournalUrl  string      `json:"journal_url" gorm:"column:journal_url"`
	UrlAlias    string      `json:"url_alias" gorm:"column:url_alias"`
}

type Journal struct {
	ID  int    `json:"id" gorm:"column:id"`
	Url string `json:"url" gorm:"column:url"`
}

// 同步信息
type SyncInfo struct {
	LastSyncTime string `json:"last_sync_time"`
	SyncCount    int    `json:"sync_count"`
}

// 搜索结果
type SearchResult struct {
	Number      interface{} `json:"number"`
	Title       string      `json:"title"`
	Description string      `json:"description"`
	Score       float64     `json:"score"`
	Url         string      `json:"url"`
}

// 响应结构
type Response struct {
	Code  int         `json:"code"`
	Msg   string      `json:"msg"`
	Data  interface{} `json:"data"`
	Total int64       `json:"total,omitempty"`
	Page  int         `json:"page,omitempty"`
	Limit int         `json:"limit,omitempty"`
}

var (
	db         *gorm.DB
	esClient   *elastic.Client
	templates  *template.Template
	journalMap map[int]string
	config     Config
)

// 加载配置文件
func loadConfig() error {
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	return nil
}

// 初始化数据库连接
func initDB() error {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s&parseTime=%v&loc=%s&timeout=%s&readTimeout=%s&writeTimeout=%s",
		config.Database.User,
		config.Database.Password,
		config.Database.Host,
		config.Database.Port,
		config.Database.DBName,
		config.Database.Charset,
		config.Database.ParseTime,
		config.Database.Loc,
		config.Database.Timeout,
		config.Database.ReadTimeout,
		config.Database.WriteTimeout,
	)

	log.Printf("正在连接数据库: %s %s", config.Database.Host, config.Database.DBName)
	var err error
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Printf("数据库连接失败: %v", err)
		return err
	}
	log.Printf("数据库连接成功")
	return nil
}

// 初始化 Elasticsearch 客户端
func initES() error {
	var err error
	esClient, err = elastic.NewClient(
		elastic.SetURL(config.Elasticsearch.URL),
		elastic.SetSniff(config.Elasticsearch.Sniff),
	)
	if err != nil {
		return err
	}
	return nil
}

// 初始化模板
func initTemplates() error {
	templates = template.Must(template.ParseFiles("index.html"))
	return nil
}

// 初始化期刊映射
func initJournalMap() error {
	var journals []Journal
	if err := db.Table(config.Table.Journal).Select("id, url").Order("url asc").Find(&journals).Error; err != nil {
		return err
	}

	journalMap = make(map[int]string)
	for _, journal := range journals {
		journalMap[journal.ID] = journal.Url
	}

	return nil
}

// 检查并创建 Elasticsearch 索引
func ensureIndexExists() error {
	ctx := context.Background()

	// 检查 manuscripts 索引
	exists, err := esClient.IndexExists(config.Index.Manuscripts).Do(ctx)
	if err != nil {
		return err
	}

	if !exists {
		// 创建 manuscripts 索引
		_, err = esClient.CreateIndex(config.Index.Manuscripts).BodyString(`{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 0,
				"analysis": {
					"analyzer": {
						"default": {
							"type": "english"
						}
					}
				}
			},
			"mappings": {
				"properties": {
					"number": {
						"type": "keyword"
					},
					"title": {
						"type": "text",
						"analyzer": "english"
					},
					"description": {
						"type": "text",
						"analyzer": "english"
					}
				}
			}
		}`).Do(ctx)
		if err != nil {
			return err
		}
	}

	// 检查 sync_info 索引
	exists, err = esClient.IndexExists(config.Index.SyncInfo).Do(ctx)
	if err != nil {
		return err
	}

	if !exists {
		// 创建 sync_info 索引
		_, err = esClient.CreateIndex(config.Index.SyncInfo).BodyString(`{
			"settings": {
				"number_of_shards": 1,
				"number_of_replicas": 0
			},
			"mappings": {
				"properties": {
					"last_sync_time": {
						"type": "date",
						"format": "yyyy-MM-dd HH:mm:ss"
					},
					"sync_count": {
						"type": "integer"
					}
				}
			}
		}`).Do(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

// 同步文章数据到 Elasticsearch
func syncManuscripts(c *gin.Context) {
	ctx := context.Background()

	// 检查数据库连接
	if db == nil {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "数据库连接失败，无法同步数据", Data: nil})
		return
	}

	// 检查索引是否存在
	if err := ensureIndexExists(); err != nil {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "确保索引存在失败: " + err.Error(), Data: nil})
		return
	}

	// 从数据库读取文章数据
	var manuscripts []Manuscript
	if err := db.Table(config.Table.Manuscript).Select("number, title, description, journal_id, url_alias").Where("is_published = ?", 1).Find(&manuscripts).Error; err != nil {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "读取文章数据失败: " + err.Error(), Data: nil})
		return
	}

	// 同步数据到 Elasticsearch
	count := 0
	for _, manuscript := range manuscripts {
		// 清理 description 字段中的 HTML 实体
		manuscript.Description = strings.TrimSpace(html.UnescapeString(manuscript.Description))

		// 构建索引请求
		// 将 manuscript.Number 转换为字符串
		id := ""
		switch v := manuscript.Number.(type) {
		case string:
			id = v
		case int:
			id = strconv.Itoa(v)
		case int64:
			id = strconv.FormatInt(v, 10)
		case float64:
			id = strconv.FormatFloat(v, 'f', 0, 64)
		default:
			id = fmt.Sprintf("%v", v)
		}

		_, err := esClient.Index().
			Index(config.Index.Manuscripts).
			Id(id).
			BodyJson(manuscript).
			Do(ctx)
		if err != nil {
			c.JSON(http.StatusOK, Response{Code: 0, Msg: "同步文章数据失败: " + err.Error(), Data: nil})
			return
		}

		count++
	}

	// 更新同步时间
	if err := updateSyncTime(); err != nil {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "更新同步时间失败: " + err.Error(), Data: nil})
		return
	}

	c.JSON(http.StatusOK, Response{Code: 1, Msg: "数据同步成功", Data: map[string]int{"total": count}})
}

// 获取期刊列表
func getJournals() ([]Journal, error) {
	var journals []Journal
	if err := db.Table(config.Table.Journal).Select("id, url").Order("url asc").Find(&journals).Error; err != nil {
		return nil, err
	}
	return journals, nil
}

// 搜索文章
func search(c *gin.Context) {
	ctx := context.Background()

	// 获取搜索关键词
	keyword := c.PostForm("keyword")
	if keyword == "" {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "请输入搜索关键词", Data: nil})
		return
	}

	// 获取分页参数
	page, _ := strconv.Atoi(c.DefaultPostForm("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultPostForm("limit", "20"))
	from := (page - 1) * limit

	// 构建搜索请求
	searchSource := elastic.NewSearchSource()
	searchSource.Query(elastic.NewMultiMatchQuery(keyword, "title", "description").Type("phrase").Analyzer("english"))
	searchSource.Highlight(elastic.NewHighlight().
		Field("title").
		Field("description").
		PreTags("<em class=\"highlight\">").
		PostTags("</em>"))
	searchSource.From(from).Size(limit)

	// 执行搜索
	result, err := esClient.Search().
		Index(config.Index.Manuscripts).
		SearchSource(searchSource).
		Do(ctx)
	if err != nil {
		c.JSON(http.StatusOK, Response{Code: 0, Msg: "搜索失败: " + err.Error(), Data: nil})
		return
	}

	// 处理搜索结果
	results := make([]SearchResult, 0, len(result.Hits.Hits))
	log.Printf("搜索结果数量: %d", len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		var manuscript Manuscript
		if err := json.Unmarshal(hit.Source, &manuscript); err != nil {
			continue
		}

		// 处理高亮
		title := manuscript.Title
		if len(hit.Highlight["title"]) > 0 {
			title = hit.Highlight["title"][0]
		}

		description := manuscript.Description
		if len(hit.Highlight["description"]) > 0 {
			description = hit.Highlight["description"][0]
		}

		// 构建 URL
		url := ""
		// 尝试从 manuscript 中获取期刊 URL 信息
		if manuscript.UrlAlias != "" {
			// 检查 manuscript 是否包含 journal_url 字段
			if manuscript.JournalUrl != "" {
				url = "https://www.xxx.com/xxx/" + manuscript.JournalUrl + "/" + manuscript.UrlAlias
			} else if journalMap != nil {
				// 如果没有 journal_url 字段，尝试从 journalMap 中获取
				if journalUrl, ok := journalMap[manuscript.JournalID]; ok {
					url = "https://www.xxx.com/xxx/" + journalUrl + "/" + manuscript.UrlAlias
				}
			} else {
				// 如果都没有，使用默认的 URL 格式
				url = "https://www.xxx.com/xxx/" + manuscript.UrlAlias
			}
		}

		// 处理 Score 类型
		score := 0.0
		if hit.Score != nil {
			score = *hit.Score
		}

		results = append(results, SearchResult{
			Number:      manuscript.Number,
			Title:       title,
			Description: description,
			Score:       score,
			Url:         url,
		})
	}

	c.JSON(http.StatusOK, Response{
		Code:  1,
		Msg:   "搜索成功",
		Data:  results,
		Total: result.Hits.TotalHits.Value,
		Page:  page,
		Limit: limit,
	})
}

// 更新同步时间
func updateSyncTime() error {
	ctx := context.Background()

	// 获取当前同步次数
	syncCount := getSyncCount() + 1

	syncInfo := SyncInfo{
		LastSyncTime: time.Now().Format("2006-01-02 15:04:05"),
		SyncCount:    syncCount,
	}

	// 更新同步信息
	_, err := esClient.Index().
		Index(config.Index.SyncInfo).
		Id("sync_info").
		BodyJson(syncInfo).
		Do(ctx)
	if err != nil {
		return err
	}

	return nil
}

// 获取同步次数
func getSyncCount() int {
	ctx := context.Background()

	exists, err := esClient.Exists().Index(config.Index.SyncInfo).Id("sync_info").Do(ctx)
	if err != nil || !exists {
		return 0
	}

	result, err := esClient.Get().Index(config.Index.SyncInfo).Id("sync_info").Do(ctx)
	if err != nil {
		return 0
	}

	var syncInfo SyncInfo
	if err := json.Unmarshal(result.Source, &syncInfo); err != nil {
		return 0
	}

	return syncInfo.SyncCount
}

// 获取上次同步时间
func getLastSyncTime() string {
	ctx := context.Background()

	exists, err := esClient.Exists().Index(config.Index.SyncInfo).Id("sync_info").Do(ctx)
	if err != nil || !exists {
		return "从未同步"
	}

	result, err := esClient.Get().Index(config.Index.SyncInfo).Id("sync_info").Do(ctx)
	if err != nil {
		return "从未同步"
	}

	var syncInfo SyncInfo
	if err := json.Unmarshal(result.Source, &syncInfo); err != nil {
		return "从未同步"
	}

	return syncInfo.LastSyncTime
}

// 首页
func index(c *gin.Context) {
	lastSyncTime := getLastSyncTime()
	c.HTML(http.StatusOK, "index.html", gin.H{
		"lastSyncTime": lastSyncTime,
	})
}

func main() {
	// 加载配置文件
	if err := loadConfig(); err != nil {
		log.Fatalf("加载配置文件失败: %v", err)
	}

	if config.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 初始化
	var dbErr error
	if err := initDB(); err != nil {
		log.Printf("初始化数据库失败: %v", err)
		dbErr = err
	}

	if err := initES(); err != nil {
		log.Fatalf("初始化 Elasticsearch 失败: %v", err)
	}

	if err := initTemplates(); err != nil {
		log.Fatalf("初始化模板失败: %v", err)
	}
	log.Printf("模板初始化成功")

	// 只有当数据库初始化成功时，才初始化期刊映射
	if dbErr == nil {
		if err := initJournalMap(); err != nil {
			log.Printf("初始化期刊映射失败: %v", err)
		}
	}

	if err := ensureIndexExists(); err != nil {
		log.Fatalf("确保索引存在失败: %v", err)
	}

	r := gin.Default()

	r.SetHTMLTemplate(templates)

	r.GET("/", index)
	r.POST("/syncManuscripts", syncManuscripts)
	r.POST("/search", search)

	r.Static("/static", ".")

	port := config.Server.Port
	fmt.Printf("服务器正在运行，监听端口 %d\n", port)
	if err := r.Run(fmt.Sprintf(":%d", port)); err != nil {
		log.Fatalf("启动服务器失败: %v", err)
	}
}
