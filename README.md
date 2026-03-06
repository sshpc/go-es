# Go-ES 文章搜索服务

一个基于Go语言开发的高效文章搜索服务，整合MySQL数据库和Elasticsearch全文搜索引擎。

> **说明**：本项目业务自用，示例代码仅供参考，实际使用需根据业务需求进行二次开发
## 项目特性

- **数据同步**：支持从MySQL数据库同步文章数据到Elasticsearch
- **全文搜索**：利用Elasticsearch提供高效的全文搜索能力
- **Web界面**：提供简洁的HTML前端进行搜索操作
- **RESTful API**：支持JSON格式的API接口
- **配置灵活**：采用YAML配置文件，便于环境切换

## 技术栈

- **后端框架**：[Gin](https://github.com/gin-gonic/gin) - 轻量级HTTP框架
- **数据库**：MySQL - 文章数据源
- **搜索引擎**：Elasticsearch - 全文搜索
- **ORM框架**：[GORM](https://gorm.io/) - 数据库操作
- **配置管理**：YAML

## 环境要求

- Go 1.20 或更高版本
- MySQL 5.7 或更高版本
- Elasticsearch 7.x 或更高版本


