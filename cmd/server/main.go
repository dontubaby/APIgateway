package main

import (
	"Skillfactory/APIGateway/internal/models"
	"context"

	"log"
	"net/http"
	"os"
	"os/signal"

	"time"

	"Skillfactory/APIGateway/internal/api"

	kfk "github.com/dontubaby/kafka_wrapper"
	middleware "github.com/dontubaby/mware"
	"github.com/joho/godotenv"
)

func main() {
	ctxmain, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := godotenv.Load()
	if err != nil {
		log.Println("cant loading .env file", err)
		return
	}
	port := os.Getenv("PORT")
	addr := "localhost:" + port

	responsesChannel := make(chan models.DetailedResponse, 2)
	//Prodcuer сообщения в Kafka который вызывает хэндлеры сервиса gonews
	p, err := kfk.NewProducer([]string{"localhost:9093"})
	if err != nil {
		log.Printf("failed to create producer: %v\n", err)
	}
	//Prodcuer сообщений в Kafka вызывающий хэндлеры добавления комментариев
	commentsAddingProducer, err := kfk.NewProducer([]string{"localhost:9093"})
	if err != nil {
		log.Printf("Kafka consumer creating error - %v", err)
	}
	//Сonsumer сообщений в Kafka который получает данные с детальной информацией о новости из сервиса gonews
	detailConsumer, err := kfk.NewConsumer([]string{"localhost:9093"}, "newsdetail")
	if err != nil {
		log.Printf("Kafka detailConsumer creating error - %v", err)
	}
	//Сonsumer сообщений в Kafka который получает список новостей из сервиса gonews
	listConsumer, err := kfk.NewConsumer([]string{"localhost:9093"}, "newslist")
	if err != nil {
		log.Printf("Kafka listConsumer creating error - %v", err)
	}
	//Сonsumer сообщений в Kafka который получает список новостей отфильтрованных по контенту
	filterContentConsumer, err := kfk.NewConsumer([]string{"localhost:9093"}, "filtered_content")
	if err != nil {
		log.Printf("Kafka filterContentConsumer creating error - %v", err)
	}
	//Сonsumer сообщений в Kafka который получает список новостей отфильтрованных по дате публикации
	filterPublishedConsumer, err := kfk.NewConsumer([]string{"localhost:9093"}, "filter_published")
	if err != nil {
		log.Printf("Kafka filterPublishedConsumer creating error - %v", err)
	}
	//Сonsumer сообщений в Kafka который получает список комментариев
	commentsConsumer, err := kfk.NewConsumer([]string{"localhost:9093"}, "comments")
	if err != nil {
		log.Printf("Kafka commentsConsumer creating error - %v", err)
	}

	api := api.New(ctxmain, p, commentsAddingProducer, detailConsumer, listConsumer, filterContentConsumer, filterPublishedConsumer, commentsConsumer, responsesChannel)
	router := api.Router()
	router.Use(middleware.RequestIDMiddleware, middleware.LoggingMiddleware)

	log.Println("Starting API gateway server at: ", addr)
	server := &http.Server{
		Addr:    addr,
		Handler: router,
	}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Listen:= %s\n", err)
		}
	}()
	//server graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	<-quit
	log.Println("Shutdown Server ...")
	ctxShutDown, _ := context.WithTimeout(context.Background(), 30*time.Second)
	server.Shutdown(ctxShutDown)
}
