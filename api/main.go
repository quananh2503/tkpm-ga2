package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	_ "github.com/lib/pq"
)

var (
	masterDB *sql.DB 
	slaveDB  *sql.DB 
	nodeID   string
)

type Product struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func main() {
	nodeID = os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "Unknown_Node"
	}

	// 1. Kết nối Master (dùng biến môi trường)
	var err error
	masterConnStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_MASTER_HOST"), os.Getenv("DB_USER"), os.Getenv("DB_PASS"), os.Getenv("DB_NAME"))
	masterDB, err = sql.Open("postgres", masterConnStr)
	if err != nil {
		log.Fatalf("Lỗi kết nối Master: %v", err)
	}

	// 2. Kết nối Slave (dùng biến môi trường)
	slaveConnStr := fmt.Sprintf("host=%s port=5432 user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("DB_SLAVE_HOST"), os.Getenv("DB_USER"), os.Getenv("DB_PASS"), os.Getenv("DB_NAME"))
	slaveDB, err = sql.Open("postgres", slaveConnStr)
	if err != nil {
		log.Fatalf("Lỗi kết nối Slave: %v", err)
	}

	// 3. Routes
	http.HandleFunc("/products", handleProducts)

	port := ":8080"
	fmt.Printf("[%s] API đang chạy tại port %s\n", nodeID, port)
	log.Fatal(http.ListenAndServe(port, nil))
}

func handleProducts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodPost {
		var p Product
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// DÙNG masterDB ĐỂ THỰC THI INSERT
		err := masterDB.QueryRow(
			"INSERT INTO products (name, price) VALUES ($1, $2) RETURNING id",
			p.Name, p.Price,
		).Scan(&p.ID)

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"message":      "Success",
			"data":         p,
			"processed_by": nodeID,
		})
		return
	}

	if r.Method == http.MethodGet {
		// DÙNG slaveDB ĐỂ SELECT
		rows, err := slaveDB.Query("SELECT id, name, price FROM products")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()

		var products []Product
		for rows.Next() {
			var p Product
			if err := rows.Scan(&p.ID, &p.Name, &p.Price); err != nil {
				continue
			}
			products = append(products, p)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"products":     products,
			"processed_by": nodeID,
		})
		return
	}

	http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
}