package main

import (
	"log"
	"net/http"

	"github.com/IEEECS-VIT/hackbattle25-backend/config"
	"github.com/IEEECS-VIT/hackbattle25-backend/routes"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func main() {
	router := mux.NewRouter()
	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Router is working!"))
	}).Methods("GET")

	config.InitFirebase()

	routes.RegisterAuthRoutes(router, config.AuthClient, config.FirestoreClient)
	routes.RegisterTeamRoutes(router)
	
	allowedOrigins := handlers.AllowedOrigins([]string{"http://localhost:3000", "http://localhost:3001", "http://localhost:3002","https://redefine-26-frontend.vercel.app","https://redefine.ieeecsvit.com})
	allowedMethods := handlers.AllowedMethods([]string{"GET", "POST", "PUT", "DELETE", "OPTIONS"})
	allowedHeaders := handlers.AllowedHeaders([]string{"Content-Type", "Authorization"})
	allowCredentials := handlers.AllowCredentials()

	log.Println("Server is running on port 8081")
	log.Fatal(http.ListenAndServe(":8081", handlers.CORS(allowedOrigins, allowedMethods, allowedHeaders, allowCredentials)(router)))
}

