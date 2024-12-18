package models

type NewsFullDetailed struct {
	ID          int64    `json:"id"`             // Уникальный идентификатор новости
	Title       string   `json:"title"`          // Заголовок новости
	Description string   `json:"description"`    // Описание новости
	Content     string   `json:"content"`        // Полный текст новости
	PublishedAt string   `json:"published_at"`   // Дата публикации
	Author      string   `json:"author"`         // Автор новости
	Tags        []string `json:"tags,omitempty"` // Массив тегов (необязательно)
}

// NewsShortDetailed содержит краткую информацию о новости
type NewsShortDetailed struct {
	ID      int64  `json:"id"`      // Уникальный идентификатор новости
	Title   string `json:"title"`   // Заголовок новости
	Preview string `json:"preview"` // Краткий анонс новости
}

// Comment содержит информацию о комментарии
type Comment struct {
	ID         int    `db:"id" json:"id"`
	News_id    int    `db:"news_id" json:"newsID"`
	Message    string `db:"comment" json:"comment"`
	Created_at int64  `db:"created_at" json:"createdAt"`
	Parrent_id int    `db:"parrent_id" json:"parentID"`
	Censore    bool   `db:"censore" json:"isCensored"`
}

type DetailedResponse struct {
	Data interface{}
	Err  error
}
type FinalResponse struct {
	News     string `json:"news"`
	Comments string `json:"comments"`
}
