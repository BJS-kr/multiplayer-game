// mutex의 대부분을 없앨 수 있을 것 같다.
package game

import (
	"coin_chase/game/item_effects"
	"coin_chase/game/owner_kind"
	"log"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"
)

func GetGameMap() *GameMap {
	if !gameMap.initialized {

		gameMap.Map = Map{
			Rows: make([]*Row, MAP_SIZE),
		}

		for i := 0; i < int(MAP_SIZE); i++ {
			gameMap.Map.Rows[i] = &Row{
				Cells: make([]*Cell, MAP_SIZE),
			}
			for j := 0; j < int(MAP_SIZE); j++ {
				gameMap.Map.Rows[i].Cells[j] = &Cell{
					Kind: owner_kind.GROUND,
				}
			}
		}

		gameMap.InitializeCoins()
		gameMap.InitializeItems()

		gameMap.initialized = true
	}

	return &gameMap
}

func (m *GameMap) StartUpdateObjectPosition(statusReceiver <-chan *Status, globalMapUpdateChannel chan EmptySignal) {
	for status := range statusReceiver {
		if status.Type == STATUS_TYPE_USER {
			if m.isOutOfRange(&status.CurrentPosition) {
				continue
			}

			if m.isOccupied(&status.CurrentPosition) {
				kind := m.Map.Rows[status.CurrentPosition.Y].Cells[status.CurrentPosition.X].Kind
				if kind == owner_kind.COIN {
					// lock을 얻었으니 MoveCoinsRandomly가 Lock을 얻지 못하고 대기해야하므로, 이곳에서의 정합성은 만족된다.
					coinIdx := slices.IndexFunc(m.coins, func(coinPosition *Position) bool {
						return coinPosition.X == status.CurrentPosition.X && coinPosition.Y == status.CurrentPosition.Y
					})

					m.coins = append(m.coins[:coinIdx], m.coins[coinIdx+1:]...)

					if len(m.coins) == 0 {
						m.InitializeCoins()
					}

					scoreboard[status.Id] += 1
				} else if kind == owner_kind.ITEM_LENGTHEN_VISION || kind == owner_kind.ITEM_SHORTEN_VISION {
					if userStatuses.StatusMap[status.Id].ResetTimer != nil {
						userStatuses.StatusMap[status.Id].ResetTimer.Stop()
					}

					userStatuses.StatusMap[status.Id].ResetTimer = time.AfterFunc(time.Second*6, func() {
						userStatuses.StatusMap[status.Id].ItemEffect = item_effects.NONE
					})
					itemIdx := slices.IndexFunc(m.randomItems, func(itemPosition *Position) bool {
						return itemPosition.X == status.CurrentPosition.X && itemPosition.Y == status.CurrentPosition.Y
					})

					if itemIdx == -1 {
						slog.Debug("Item exists but not found in slice")
					}

					m.randomItems = append(m.randomItems[:itemIdx], m.randomItems[itemIdx+1:]...)

					if len(m.randomItems) == 0 {
						m.InitializeItems()
					}

					if kind == owner_kind.ITEM_LENGTHEN_VISION {
						// UserStatuses를 변조하고 있으나, 변조하는 스레드들이 각자 RWMutexMap의 Lock을 얻어야하므로 상관없다.
						userStatuses.StatusMap[status.Id].ItemEffect = item_effects.LENGTHEN
					} else if kind == owner_kind.ITEM_SHORTEN_VISION {
						userStatuses.StatusMap[status.Id].ItemEffect = item_effects.SHORTEN
					}
				} else {
					log.Fatal("invalid occupied object found")
				}
			}
			// 이 currentPosition은 서버에 저장된 user의 위치 정보로, userStatus.CurrentPosition과는 다른 값이다.
			currentPosition, exists := userStatuses.GetUserPosition(status.Id)

			if exists {
				m.Map.Rows[currentPosition.Y].Cells[currentPosition.X] = &Cell{
					Occupied: false,
					Owner:    "",
					Kind:     owner_kind.GROUND,
				}
			}

			userStatuses.SetUserPosition(status.Id, status.CurrentPosition.X, status.CurrentPosition.Y)

			m.Map.Rows[status.CurrentPosition.Y].Cells[status.CurrentPosition.X] = &Cell{
				Occupied: true,
				Owner:    status.Id,
				Kind:     owner_kind.USER,
			}
		} else if status.Type == STATUS_TYPE_COIN {
			m.MoveCoinsRandomly()
		}

		globalMapUpdateChannel <- Signal
	}
}

func (m *GameMap) GetRelatedPositions(userPosition *Position, visibleRange int32) []*RelatedPosition {
	surroundedPositions := make([]Position, 0)

	for x := -visibleRange; x <= visibleRange; x++ {
		for y := -visibleRange; y <= visibleRange; y++ {
			if x == 0 && y == 0 {
				continue // 자신의 위치임
			}
			surroundedPositions = append(surroundedPositions, Position{
				X: userPosition.X + x,
				Y: userPosition.Y + y,
			})
		}
	}

	relatedPositions := make([]*RelatedPosition, 0)
	for _, surroundedPosition := range surroundedPositions {
		if m.isOutOfRange(&surroundedPosition) {
			continue
		}
		relatedPosition := RelatedPosition{
			Position: &surroundedPosition,
			Cell:     m.Map.Rows[surroundedPosition.Y].Cells[surroundedPosition.X],
		}
		relatedPositions = append(relatedPositions, &relatedPosition)
	}

	return relatedPositions
}

func (m *GameMap) isOutOfRange(position *Position) bool {
	return position.X > MAP_SIZE-1 ||
		position.Y > MAP_SIZE-1 ||
		position.X < 0 ||
		position.Y < 0
}

func (m *GameMap) isOccupied(position *Position) bool {
	return m.Map.Rows[position.Y].Cells[position.X].Occupied
}
func (m *GameMap) InitializeItems() {
	m.randomItems = make([]*Position, 0)
	// item은 coin과 다르게 항상 ITEM_COUNT만큼 생성되어야 한다.
	toGenerate := ITEM_COUNT

	for toGenerate > 0 {
		x, y := rand.Int32N(MAP_SIZE), rand.Int32N(MAP_SIZE)
		if m.Map.Rows[y].Cells[x].Occupied {
			continue
		}

		itemCell := &Cell{
			Occupied: true,
			Owner:    OWNER_SYSTEM,
		}

		if GenerateRandomDirection() == 1 {
			itemCell.Kind = owner_kind.ITEM_LENGTHEN_VISION
		} else {
			itemCell.Kind = owner_kind.ITEM_SHORTEN_VISION
		}

		m.Map.Rows[y].Cells[x] = itemCell
		m.randomItems = append(m.randomItems, &Position{
			X: x,
			Y: y,
		})
		toGenerate--
	}
}
func (m *GameMap) InitializeCoins() {
	m.coins = make([]*Position, 0)
	for i := 0; i < COIN_COUNT; i++ { // 겹칠 수 있으니 코인의 갯수도 랜덤(Occupied되지 않은 곳에만 생성하니까). 즉 COIN_COUNT보다 적게 생성 될 수도 있다.
		x, y := rand.Int32N(MAP_SIZE), rand.Int32N(MAP_SIZE)
		if !m.Map.Rows[y].Cells[x].Occupied {
			m.Map.Rows[y].Cells[x] = &Cell{
				Occupied: true,
				Owner:    OWNER_SYSTEM,
				Kind:     owner_kind.COIN,
			}
			m.coins = append(m.coins, &Position{
				X: x,
				Y: y,
			})
		}
	}
}

func (m *GameMap) CountCoins() int {
	return len(m.coins)
}

func (m *GameMap) CountItems() int {
	return len(m.randomItems)
}

func (m *GameMap) MoveCoinsRandomly() {
	for i, coinPosition := range m.coins {
		newPos := &Position{
			X: coinPosition.X + GenerateRandomDirection(),
			Y: coinPosition.Y + GenerateRandomDirection(),
		}

		if m.isOutOfRange(newPos) || m.isOccupied(newPos) {
			continue
		}

		m.Map.Rows[coinPosition.Y].Cells[coinPosition.X] = &Cell{
			Occupied: false,
			Owner:    "",
			Kind:     owner_kind.GROUND,
		}

		m.Map.Rows[newPos.Y].Cells[newPos.X] = &Cell{
			Occupied: true,
			Owner:    OWNER_SYSTEM,
			Kind:     owner_kind.COIN,
		}

		m.coins[i] = newPos
	}
}
