package resource_handler

import (
	"time"

	"github.com/jeffail/tunny"
	"github.com/olebedev/emitter"
	"github.com/patrickmn/go-cache"
)

type ResourceHandler struct {
	pool      *tunny.WorkPool
	eventBus  *emitter.Emitter
	itemCache *cache.Cache
}

type resource struct {
	isComplete bool
	result     interface{}
}

type WorkRequest struct {
	Id       string
	Metadata interface{}
}

func New(workers int, fetchFn func(object *WorkRequest) interface{}) (*ResourceHandler, error) {
	workFn := func(i interface{}) interface{} { return fetchFn(i.(*WorkRequest)) }
	pool, err := tunny.CreatePool(workers, workFn).Open()
	if err != nil {
		return nil, err
	}

	bus := &emitter.Emitter{}
	itemCache := cache.New(1*time.Minute, 5*time.Minute) // store for 1min, clean up every 5min

	handler := &ResourceHandler{pool, bus, itemCache}
	return handler, nil
}

func (h *ResourceHandler) Close() {
	h.pool.Close()
}

func (h *ResourceHandler) GetResource(id string, metadata interface{}) (chan interface{}) {
	resultChan := make(chan interface{})

	// First see if we have already cached this request
	cachedResource, found := h.itemCache.Get(id)
	if found {
		res := cachedResource.(*resource)

		// If the request has already been completed, return that result
		if res.isComplete {
			resultChan <- res.result
			return resultChan
		}

		// Otherwise queue a wait function to handle the resource when it is complete
		go func() {
			result := <-h.eventBus.Once("complete_" + id)
			resultChan <- result.Args[0]
		}()

		return resultChan
	}

	// Cache that we're starting the request (never expire)
	h.itemCache.Set(id, &resource{false, nil}, cache.NoExpiration)

	go func() {
		// Queue the work (ignore errors)
		result, _ := h.pool.SendWork(&WorkRequest{id, metadata})
		h.eventBus.Emit("complete_"+id, result)

		// Cache the result for future callers
		newResource := &resource{
			isComplete: true,
			result:     result,
		}
		h.itemCache.Set(id, newResource, cache.DefaultExpiration)

		// and finally feed it back to the caller
		resultChan <- result
	}()

	return resultChan
}
