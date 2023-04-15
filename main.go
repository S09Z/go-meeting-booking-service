package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/dgrijalva/jwt-go"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type MeetingRoom struct {
	ID       uint      `gorm:"primaryKey"`
	Name     string    `gorm:"not null"`
	Capacity int       `gorm:"not null"`
	Bookings []Booking `gorm:"foreignKey:MeetingRoomID"`
}

type Booking struct {
	ID            string    `gorm:"primaryKey"`
	Start         time.Time `gorm:"not null"`
	End           time.Time `gorm:"not null"`
	MeetingRoom   MeetingRoom
	MeetingRoomID string
	CreatedBy     string `gorm:"not null"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthToken struct {
	Token string `json:"token"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type Claims struct {
	Username string `json:"username"`
	jwt.StandardClaims
}

func main() {
	e := echo.New()

	// Initialize Redis
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	defer redisClient.Close()

	// Initialize GORM
	dsn := "host=localhost user=postgres password=mysecretpassword dbname=booking_webservice port=5432 sslmode=disable TimeZone=Asia/Bangkok"
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		panic("failed to connect database")
	}
	sqlDB, err := db.DB()
	if err != nil {
		panic("failed to connect database")
	}
	defer sqlDB.Close()

	// Migrate the schema
	err = db.AutoMigrate(&MeetingRoom{}, &Booking{})
	if err != nil {
		panic("failed to migrate database")
	}

	// Middleware
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			authHeader := c.Request().Header.Get("Authorization")
			if authHeader == "" {
				return c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Missing authorization header"})
			}

			tokenString := authHeader[len("Bearer "):]
			claims := &Claims{}

			token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
				// Check token signing method
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				// Return secret key
				return []byte(os.Getenv("JWT_SECRET")), nil
			})

			if err != nil {
				if err == jwt.ErrSignatureInvalid {
					return c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid token signature"})
				}
				return c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid token"})
			}

			if !token.Valid {
				return c.JSON(http.StatusUnauthorized, ErrorResponse{Error: "Invalid token"})
			}

			return next(c)
		}
	})

	// Routes
	e.POST("/login", func(c echo.Context) error {
		req := &LoginRequest{}
		if err := c.Bind(req); err != nil {
			return c.JSON(http.StatusBadRequest, ErrorResponse{Error: "Invalid request body"})
		}

		// Check if username and password are valid
		if req.Username == "admin" && req.Password == "password" {
			// Create JWT token
			token := jwt.New(jwt.SigningMethodHS256)

			// Set token claims
			claims := token.Claims.(jwt.MapClaims)
			claims["username"] = req.Username
			claims["exp"] = time.Now().Add(time.Hour * 24).Unix()

			// Generate encoded token and send it as response
			t, err := token.SignedString([]byte("secret"))
			if err != nil {
				return err
			}
			return c.JSON(http.StatusOK, AuthToken{Token: t})
		}

		return echo.ErrUnauthorized
	})

	e.POST("/meeting_rooms/:id/bookings", func(c echo.Context) error {
		// Get meeting room ID from path parameter
		meetingRoomID := c.Param("id")

		// Get start and end time from request body
		startTime, err := time.Parse(time.RFC3339, c.FormValue("start"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid start time format")
		}
		endTime, err := time.Parse(time.RFC3339, c.FormValue("end"))
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid end time format")
		}

		// Check if meeting room is available
		var bookings []Booking
		result := db.Where("meeting_room_id = ? AND start <= ? AND end >= ?", meetingRoomID, startTime, endTime).Find(&bookings)
		if result.Error != nil {
			return result.Error
		}

		if len(bookings) > 0 {
			return echo.NewHTTPError(http.StatusConflict, "Meeting room is not available")
		}

		// Get username from JWT token
		user := c.Get("user").(*jwt.Token)
		claims := user.Claims.(jwt.MapClaims)
		username := claims["username"].(string)

		// Create new booking in database
		booking := Booking{
			ID:            uuid.New().String(),
			Start:         startTime,
			End:           endTime,
			MeetingRoomID: meetingRoomID,
			CreatedBy:     username,
		}

		result = db.Create(&booking)
		if result.Error != nil {
			return result.Error
		}

		return c.JSON(http.StatusOK, booking)
	})
}
