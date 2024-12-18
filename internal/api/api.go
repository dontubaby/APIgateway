package api

import (
	"Skillfactory/APIGateway/internal/models"

	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	kfk "github.com/dontubaby/kafka_wrapper"
	"github.com/gorilla/mux"
)

const (
	censorServiceAddress = "http://localhost:4000/censor/"
)

// Структура Api TODO: преобразовать поля консьюмеров и продюсеров в map
type Api struct {
	r                       *mux.Router
	newsProducer            *kfk.Producer
	commentProducer         *kfk.Producer
	detailConsumer          *kfk.Consumer
	listConsumer            *kfk.Consumer
	filterContentConsumer   *kfk.Consumer
	filterPublishedConsumer *kfk.Consumer
	commentsConsumer        *kfk.Consumer
	responsersChannel       chan models.DetailedResponse
	context                 context.Context
}

// Конструктор API
func New(ctx context.Context, newsProd, commentProd *kfk.Producer, detailCons, listCons, filterContentCons, filterPublishedCons, commentsCons *kfk.Consumer, responsesChannel chan models.DetailedResponse) *Api {
	api := Api{r: mux.NewRouter(),
		newsProducer:            newsProd,
		commentProducer:         commentProd,
		detailConsumer:          detailCons,
		listConsumer:            listCons,
		filterContentConsumer:   filterContentCons,
		filterPublishedConsumer: filterPublishedCons,
		commentsConsumer:        commentsCons,
		responsersChannel:       responsesChannel,
		context:                 ctx,
	}
	//регистрация эндпоинтов
	api.endpoints()
	return &api
}

// инициализация роутера API
func (api *Api) Router() *mux.Router {
	return api.r
}
func (api *Api) endpoints() {
	//тестовый маршрут для проверки работы сервера сервиса по корневому эндпоинту
	api.r.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("GoNews Server")) }).Methods(http.MethodGet, http.MethodOptions)
	//Маршрут для редиректа в сервис gonews для вызова хэндлера предоставляющего список новостей
	api.r.HandleFunc("/newslist/", NewsListRedirectHandler(api.context, api.listConsumer, api.newsProducer)).Methods(http.MethodGet, http.MethodOptions)
	//Маршрут для редиректа в сервис gonews для вызова хэндлера предоставляющего список отфильтрованных по содержанию контента
	api.r.HandleFunc("/newslist/filtered/", filterByContentRedirectHandler(api.context, api.filterContentConsumer, api.newsProducer)).Methods(http.MethodGet, http.MethodOptions)
	//Маршрут для редиректа в сервис gonews для вызова хэндлера предоставляющего список отфильтрованных по дате публикации
	api.r.HandleFunc("/newslist/filtered/date/", filterByPublishedRedirectHandler(api.context, api.filterPublishedConsumer, api.newsProducer)).Methods(http.MethodGet, http.MethodOptions)
	// Маршрут для редиректа в сервис comments для вызова хэндлера предоставляющего список всех комментариев к новости
	api.r.HandleFunc("/addcomment/", AddCommentRedirectHandler(api.context, api.commentProducer)).Methods(http.MethodPost, http.MethodOptions)
	//Маршрут для вовзрата обобщенной информации о новости (данные о новости + комментарии к ней)
	api.r.HandleFunc("/newsdetail/{id}", combinedNewsCommentsRedirectHandler(api.detailConsumer, api.commentsConsumer, api.newsProducer)).Methods(http.MethodGet, http.MethodOptions)
}

// Функция распределения ответов от разных сервисов
func combineResponses(ch <-chan models.DetailedResponse) (models.FinalResponse, error) {
	var finalResponse models.FinalResponse
	for response := range ch {
		v, ok := response.Data.(string)
		if !ok {
			return models.FinalResponse{}, fmt.Errorf("unexpected response type not ok: %T", v)
		}
		if strings.Contains(v, "Title") {
			finalResponse.News = v
		}
		if strings.Contains(v, "comment") {
			finalResponse.Comments = v
		}
	}
	return finalResponse, nil
}

// Хэндлер для возврата обобщенной информации о новости (данные новости + комментарии к ней)
func combinedNewsCommentsRedirectHandler(detailConsumer, commentsConsumer *kfk.Consumer, p *kfk.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		newsID := mux.Vars(r)["id"]

		ch := make(chan models.DetailedResponse, 2)
		var wg sync.WaitGroup
		wg.Add(2)

		childCtx, cancel := context.WithCancel(context.Background())
		defer cancel()

		//горутина получения детальной информации о новости
		go func() {
			defer wg.Done()
			err := detailedNewsRedirectHandler(childCtx, newsID, detailConsumer, p, ch)
			if err != nil {
				log.Printf("failed to get detailed news: %v\n", err)
				cancel()
				response := models.DetailedResponse{Err: err}
				select {
				case ch <- response:
					ch <- response
				default:
				}
			}
		}()
		//горутина получения комментариев к конкретной новости
		go func() {
			defer wg.Done()
			err := CommentsListRedirectHandler(childCtx, newsID, commentsConsumer, p, ch)
			if err != nil {
				log.Printf("failed to get comments list: %v\n", err)
				cancel()
				response := models.DetailedResponse{Err: err}
				select {
				case ch <- response:
					ch <- response
				default:
				}
			}
		}()
		wg.Wait()
		close(ch)

		finalResponse, err := combineResponses(ch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(fmt.Sprint(finalResponse)))
		w.WriteHeader(http.StatusOK)
	}
}

// Хэндлер для редиректа на сервер сервиса gonews для записи детальной информации о новости в канал для последующего комбинирования с ответом сервиса комментариев и возврата клиенту
func detailedNewsRedirectHandler(ctx context.Context, newsID string, detailConsumer *kfk.Consumer, p *kfk.Producer, ch chan<- models.DetailedResponse) error {
	err := p.SendMessage(ctx, "news_input", []byte("/newsdetail/"+newsID))
	if err != nil {
		return fmt.Errorf("failed to write message in Kafka: %w", err)
	}
	msg, err := detailConsumer.GetMessages(ctx)
	if err != nil {
		return fmt.Errorf("error when reading message from Kafka: %w", err)
	}
	newsData := models.DetailedResponse{Data: string(msg.Value)}
	ch <- newsData
	return nil
}

// Хэндлер для редиректа на сервер сервиса gonews для возврата списка новостей
func NewsListRedirectHandler(ctx context.Context, c *kfk.Consumer, p *kfk.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		param := r.URL.Query().Get("n")
		err := p.SendMessage(r.Context(), "news_input", []byte("/newslist/?n="+param))
		if err != nil {
			log.Printf("failed to write message in Kafka: %v\n", err)
		}
		msg, err := c.GetMessages(ctx)
		w.Write([]byte(string(msg.Value)))
		if err != nil {
			log.Printf("error when reading message fron Kafka - %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// Хэндлер для редиректа на сервер сервиса gonews для возврата списка новостей отфильтрованных по контенту
func filterByContentRedirectHandler(ctx context.Context, c *kfk.Consumer, p *kfk.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		param := r.URL.Query().Get("s")
		fmt.Println("query parameter:", param)
		err := p.SendMessage(r.Context(), "news_input", []byte("/newslist/filtered/?s="+param))
		if err != nil {
			log.Printf("failed to write message in Kafka: %v\n", err)
		}
		msg, err := c.GetMessages(ctx)
		w.Write([]byte(string(msg.Value)))
		log.Printf("Received message from Kafka: %s Topic: %s\n", string(msg.Value), msg.Topic)
		if err != nil {
			log.Printf("error when reading message fron Kafka - %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// Хэндлер для редиректа на сервер сервиса gonews для возврата списка новостей отфильтрованных по дате публикации
func filterByPublishedRedirectHandler(ctx context.Context, c *kfk.Consumer, p *kfk.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		param := r.URL.Query().Get("date")
		err := p.SendMessage(r.Context(), "news_input", []byte("/newslist/filtered/date/?date="+param))
		if err != nil {
			log.Printf("failed to write message in Kafka: %v\n", err)
		}
		msg, err := c.GetMessages(ctx)
		w.Write([]byte(string(msg.Value)))
		if err != nil {
			log.Printf("error when reading message fron Kafka - %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}
}

// Хэндлер для редиректа в сервис comments для вызова хэндлера предоставляющего список всех комментариев к новости
func CommentsListRedirectHandler(ctx context.Context, newsID string, c *kfk.Consumer, p *kfk.Producer, ch chan<- models.DetailedResponse) error {
	err := p.SendMessage(ctx, "comments_input", []byte("/comments/"+newsID))
	if err != nil {
		return fmt.Errorf("failed to write message in Kafka: %w", err)
	}
	msg, err := c.GetMessages(ctx)
	if err != nil {
		return fmt.Errorf("error when reading message from Kafka: %w", err)
	}
	commentsData := models.DetailedResponse{Data: string(msg.Value)}
	ch <- commentsData
	return nil
}

// Хэндлер для редиректа в сервис comments для последующего добавления новости
func AddCommentRedirectHandler(ctx context.Context, p *kfk.Producer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", http.MethodPost)
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusOK)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		var comment models.Comment
		err = json.Unmarshal(body, &comment)
		fmt.Printf("id: %v,comment: %v, parrent: %v\n", comment.News_id, comment.Message, comment.Parrent_id)

		//редирект в сервис проверки цензуры комментария
		req, err := http.NewRequest(http.MethodPost, censorServiceAddress, bytes.NewBuffer([]byte(comment.Message)))
		if err != nil {
			http.Error(w, "failed redirect request", http.StatusBadRequest)
			return
		}
		client := &http.Client{}
		resp, err := client.Do(req)

		if err != nil {
			http.Error(w, "failed redirect request", http.StatusBadRequest)
			return
		}
		//Если ответ сервиса цензуры - 400 то комментарии содержит запрещенные слова и не может быть добавлен
		if resp.StatusCode == http.StatusBadRequest {
			http.Error(w, "failed! comment contains forbidden words!", http.StatusBadRequest)
			return
			//Если ответ сервиса цензуры - 200 то комментарии не содержит запрещенные слова может быть добавлен
		} else if resp.StatusCode == http.StatusOK {
			err = p.SendMessage(r.Context(), "add_comments", body)
			if err != nil {
				log.Printf("failed to write message in Kafka: %v\n", err)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte("The comment was successfully added!"))
		}
	}
}
