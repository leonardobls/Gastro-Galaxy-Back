package database

import (
	"context"
	"database/sql"
	"fmt"
	"gastro-galaxy-back/internal/models"
	"log"
	"os"
	"strconv"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/joho/godotenv/autoload"
)

// Service represents a service that interacts with a database.
type Service interface {
	// Health returns a map of health status information.
	// The keys and values in the map are service-specific.
	Health() map[string]string

	// Close terminates the database connection.
	// It returns an error if the connection cannot be closed.
	Close() error

	InsertRecipe(name string, description string, longDescription string, url string, categoryId int, ingredientIds []int) (int, error)
	UpdateRecipe(id int, name string, description string, url string) error
	InsertRecipeIngredient(recipeId int, ingredientIds []int) error
	GetRecipes(category string) ([]models.Recipe, error)
	GetRecipeWithIngredients(recipeId int) (*models.RecipeWithIngredientsDto, error)
	InsertIngredient(name string, amount string, url string, isAvailable bool) (int, error)
	GetIngredients() (*[]models.Ingedient, error)
}

type service struct {
	db *sql.DB
}

var (
	database   = os.Getenv("DB_DATABASE")
	password   = os.Getenv("DB_PASSWORD")
	username   = os.Getenv("DB_USERNAME")
	port       = os.Getenv("DB_PORT")
	host       = os.Getenv("DB_HOST")
	dbInstance *service
)

func New() Service {
	// Reuse Connection
	if dbInstance != nil {
		return dbInstance
	}
	connStr := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", username, password, host, port, database)
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		log.Fatal(err)
	}
	dbInstance = &service{
		db: db,
	}
	return dbInstance
}

// Health checks the health of the database connection by pinging the database.
// It returns a map with keys indicating various health statistics.
func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := make(map[string]string)

	// Ping the database
	err := s.db.PingContext(ctx)
	if err != nil {
		stats["status"] = "down"
		stats["error"] = fmt.Sprintf("db down: %v", err)
		log.Fatalf(fmt.Sprintf("db down: %v", err)) // Log the error and terminate the program
		return stats
	}

	// Database is up, add more statistics
	stats["status"] = "up"
	stats["message"] = "It's healthy"

	// Get database stats (like open connections, in use, idle, etc.)
	dbStats := s.db.Stats()
	stats["open_connections"] = strconv.Itoa(dbStats.OpenConnections)
	stats["in_use"] = strconv.Itoa(dbStats.InUse)
	stats["idle"] = strconv.Itoa(dbStats.Idle)
	stats["wait_count"] = strconv.FormatInt(dbStats.WaitCount, 10)
	stats["wait_duration"] = dbStats.WaitDuration.String()
	stats["max_idle_closed"] = strconv.FormatInt(dbStats.MaxIdleClosed, 10)
	stats["max_lifetime_closed"] = strconv.FormatInt(dbStats.MaxLifetimeClosed, 10)

	// Evaluate stats to provide a health message
	if dbStats.OpenConnections > 40 { // Assuming 50 is the max for this example
		stats["message"] = "The database is experiencing heavy load."
	}

	if dbStats.WaitCount > 1000 {
		stats["message"] = "The database has a high number of wait events, indicating potential bottlenecks."
	}

	if dbStats.MaxIdleClosed > int64(dbStats.OpenConnections)/2 {
		stats["message"] = "Many idle connections are being closed, consider revising the connection pool settings."
	}

	if dbStats.MaxLifetimeClosed > int64(dbStats.OpenConnections)/2 {
		stats["message"] = "Many connections are being closed due to max lifetime, consider increasing max lifetime or revising the connection usage pattern."
	}

	return stats
}

func (s *service) InsertRecipe(name string, description string, longDescription string, url string, categoryId int, ingredientIds []int) (int, error) {

	log.Printf("Inserting new recipe")
	stmt := `INSERT INTO recipe (name, description, long_description, imageurl, category_id) VALUES($1,$2,$3,$4,$5) RETURNING id`

	var id int

	err := s.db.QueryRow(stmt, name, description, longDescription, url, categoryId).Scan(&id)

	if err != nil {
		return -1, err
	}

	for _, ingredientId := range ingredientIds {
		stmt := `INSERT INTO ingredient_recipe (ingredient_id, recipe_id) VALUES($1,$2)`
		_, err = s.db.Exec(stmt, ingredientId, id)

		if err != nil {
			return int(id), err
		}
	}

	err = s.InsertRecipeIngredient(id, ingredientIds)

	if err != nil {
		return int(id), err
	}

	return int(id), nil
}

func (s *service) GetRecipes(category string) ([]models.Recipe, error) {

	baseQuery := `
        SELECT r.id, r.name, r.description, r.long_description, r.imageurl, r.category_id
        FROM recipe r
    `
	var rows *sql.Rows
	var err error

	if category != "" {
		query := baseQuery + `
        JOIN category c ON r.category_id = c.id
        WHERE c.name = $1
        `
		rows, err = s.db.Query(query, category)
	} else {
		rows, err = s.db.Query(baseQuery)
	}

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var recipes []models.Recipe

	for rows.Next() {
		var recipe models.Recipe
		if err := rows.Scan(&recipe.Id, &recipe.Name, &recipe.Description, &recipe.LongDescription, &recipe.Url, &recipe.CategoryId); err != nil {
			return nil, err
		}
		recipes = append(recipes, recipe)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return recipes, nil
}

func (s *service) GetRecipeWithIngredients(recipeId int) (*models.RecipeWithIngredientsDto, error) {

	log.Printf("Getting recipe with ingredients")

	recipeQuery := `
		SELECT r.id, r.name, r.description, r.long_description, r.imageurl, r.category_id
		FROM recipe r
		WHERE r.id = $1
	`

	var recipe models.Recipe

	err := s.db.QueryRow(recipeQuery, recipeId).Scan(&recipe.Id, &recipe.Name, &recipe.Description, &recipe.LongDescription, &recipe.Url, &recipe.CategoryId)

	if err != nil {
		return nil, err
	}

	ingredientQuery := `
		SELECT i.id, i.name, i.amount, i.imageUrl, i.isAvailable
		FROM ingredient i
		INNER JOIN ingredient_recipe ir ON i.id = ir.ingredient_id WHERE ir.recipe_id = $1
	`
	rows, err := s.db.Query(ingredientQuery, recipeId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ingredients []models.Ingedient

	for rows.Next() {
		var ingredient models.Ingedient

		if err := rows.Scan(&ingredient.Id, &ingredient.Name, &ingredient.Amount, &ingredient.Url, &ingredient.IsAvailable); err != nil {
			return nil, err
		}

		ingredients = append(ingredients, ingredient)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &models.RecipeWithIngredientsDto{
		Recipe:      recipe,
		Ingredients: ingredients,
	}, nil

}

func (s *service) InsertIngredient(name string, amount string, url string, isAvailable bool) (int, error) {

	log.Printf("Inserting new ingredient")
	stmt := `INSERT INTO ingredient (name, amount, imageurl, isavailable) VALUES($1,$2,$3,$4) RETURNING id`

	var id int

	err := s.db.QueryRow(stmt, name, amount, url, isAvailable).Scan(&id)

	if err != nil {
		return -1, err
	}

	return int(id), nil
}

func (s *service) UpdateRecipe(id int, name string, description string, url string) error {

	updateRecipeQuery := `
		UPDATE recipe 
		SET name = $2, description = $3, imageurl = $4
		WHERE id = $1
	`

	_, err := s.db.Exec(updateRecipeQuery, id, name, description, url)

	if err != nil {
		return err
	}

	return nil
}

func (s *service) InsertRecipeIngredient(recipeId int, ingredientIds []int) error {

	for _, ingredientId := range ingredientIds {
		stmt := `INSERT INTO ingredient_recipe (ingredient_id, recipe_id) VALUES($1,$2)`
		_, err := s.db.Exec(stmt, ingredientId, recipeId)

		if err != nil {
			return err
		}
	}

	return nil
}

func (s *service) GetIngredients() (*[]models.Ingedient, error) {

	getIngredientsQuery := `SELECT i.id, i.name, i.amount, i.imageUrl, i.isavailable FROM ingredient i`

	rows, err := s.db.Query(getIngredientsQuery)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var ingredients []models.Ingedient

	for rows.Next() {

		var ingredient models.Ingedient

		if err := rows.Scan(&ingredient.Id, &ingredient.Name, &ingredient.Amount, &ingredient.Url, &ingredient.IsAvailable); err != nil {
			return nil, err
		}

		ingredients = append(ingredients, ingredient)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &ingredients, err
}

// Close closes the database connection.
// It logs a message indicating the disconnection from the specific database.
// If the connection is successfully closed, it returns nil.
// If an error occurs while closing the connection, it returns the error.
func (s *service) Close() error {
	log.Printf("Disconnected from database: %s", database)
	return s.db.Close()
}
