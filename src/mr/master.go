package mr

import (
	"fmt"
	"io/ioutil"
	"log"
	"regexp"
	"strconv"
	"sync"
	"time"
)
import "net"
import "os"
import "net/rpc"
import "net/http"

type Master struct {
	// Your definitions here.
	MapExecuting    map[string]*Task // 文件名->task
	ReduceExecuting map[string]*Task

	MapUnExecute    []*Task
	ReduceUnExecute []*Task

	MapExecuted    map[string]*Task
	ReduceExecuted map[string]*Task

	mu sync.Mutex

	NReduce int

	LockForUpdate sync.Mutex
}

// Your code here -- RPC handlers for the worker to call.
func (m *Master) RetrieveTask(args *AskForTaskArgs, reply *AskForTaskReply) error {
	//加锁
	m.mu.Lock()
	defer m.mu.Unlock()

	fmt.Println("unexecute:" + strconv.Itoa(len(m.MapUnExecute)) + "\n" + "executing:" + strconv.Itoa(len(m.MapExecuting)))
	fmt.Println("reduce_unexecute:" + strconv.Itoa(len(m.ReduceUnExecute)) + "\n" + "reduce_executing:" + strconv.Itoa(len(m.ReduceExecuting)))

	//如果reduce都执行完了
	reduceFinished := m.ReduceUnExecute != nil && len(m.ReduceUnExecute) == 0 && len(m.ReduceExecuting) == 0
	if reduceFinished {
		reply.Status = ASK_FOR_TASK_DONE
		return nil
	}

	//如果没有reduce任务了,返回状态码
	if m.ReduceUnExecute != nil && len(m.ReduceUnExecute) == 0 {
		reply.Status = ASK_FOR_TASK_FAIL
		return nil
	}

	reply.Status = ASK_FOR_TASK_SUCCESS
	//如果map任务都执行完了 , 就分发reduce任务 , 第一次先初始化
	mapFinished := len(m.MapUnExecute) == 0 && len(m.MapExecuting) == 0

	if mapFinished && m.ReduceUnExecute == nil {
		//初始化未执行ReduceTask数组
		m.InitReduceTask()
	}

	if mapFinished {
		//取出reduce一个任务
		task := m.ReduceUnExecute[0]
		m.ReduceUnExecute = m.ReduceUnExecute[1:]
		//放入执行中
		m.ReduceExecuting[task.FileName] = task
		//返回给客户端
		reply.Task = task
		return nil
	}

	//如果没有map任务了,返回状态码
	if m.MapUnExecute != nil && len(m.MapUnExecute) == 0 {
		reply.Status = ASK_FOR_TASK_FAIL
		return nil
	}
	//取出一个任务
	task := m.MapUnExecute[0]
	m.MapUnExecute = m.MapUnExecute[1:]
	//放入执行中
	m.MapExecuting[task.FileName] = task

	//返回给客户端
	reply.Task = task

	return nil
}

func (m *Master) InitReduceTask() {
	m.ReduceUnExecute = []*Task{}
	reduceFiles := []string{}
	//for i := 0; i < m.NReduce; i++ {
	//	reduceFiles = append(reduceFiles, "mr-mid-"+strconv.Itoa(i))
	//}
	files, err := ioutil.ReadDir("./")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		if match, _ := regexp.MatchString("mr-mid-*", f.Name()); match {
			reduceFiles = append(reduceFiles, f.Name())
		}
	}
	for _, file := range reduceFiles {
		m.ReduceUnExecute = append(m.ReduceUnExecute, &Task{
			Type:         TYPE_REDUCE,
			FileName:     file,
			Status:       WAIT_FOR_EXECUTE,
			RetrieveTime: time.Now(),
			NReduce:      m.NReduce,
		})
	}
	//初始化 map
	m.ReduceExecuted = make(map[string]*Task)
	m.ReduceExecuting = make(map[string]*Task)
}

func (m *Master) UpdateTaskFinished(args *TaskFinishedArgs, reply *TaskFinishedReply) error {
	m.LockForUpdate.Lock()
	defer m.LockForUpdate.Unlock()
	if args.Task.isTaskExecuted(m) {
		reply.Status = TASK_ALREADY_EXECUTED
		return nil
	}

	reply.Status = TASK_NOT_EXECUTED
	switch args.Task.Type {
	case TYPE_REDUCE:
		task, ok := m.ReduceExecuting[args.Task.FileName]
		if !ok {
			reply.Status = TASK_ALREADY_EXECUTED
			return nil
		}

		task.Status = EXECUTED
		task.FinishedTime = args.Task.FinishedTime

		delete(m.ReduceExecuting, task.FileName)

		m.ReduceExecuted[task.FileName] = task
		return nil
	case TYPE_MAP:
		task, ok := m.MapExecuting[args.Task.FileName]
		if !ok {
			reply.Status = TASK_ALREADY_EXECUTED
			return nil
		}

		task.Status = EXECUTED
		task.FinishedTime = args.Task.FinishedTime

		delete(m.MapExecuting, task.FileName)

		m.MapExecuted[task.FileName] = task

	}

	return nil
}

func (m *Master) IsTaskExecuted(args *TaskExecutedArgs, reply *TaskExecutedReply) error {
	task := args.Task
	table := task.getTaskExecuted(m)

	if _, ok := table[task.FileName]; ok {
		reply.Status = TASK_ALREADY_EXECUTED
	} else {
		reply.Status = TASK_NOT_EXECUTED
	}
	return nil
}

//
// an example RPC handler.
//
// the RPC argument and reply types are defined in rpc.go.
//
func (m *Master) Example(args *ExampleArgs, reply *ExampleReply) error {
	reply.Y = args.X + 1
	return nil
}

//
// start a thread that listens for RPCs from worker.go
//
func (m *Master) server() {
	rpc.Register(m)
	rpc.HandleHTTP()
	//l, e := net.Listen("tcp", ":1234")
	sockname := masterSock()
	os.Remove(sockname)
	l, e := net.Listen("unix", sockname)
	if e != nil {
		log.Fatal("listen error:", e)
	}
	go func() {
		for {
			for _, task := range m.MapExecuting {
				if task.isTimeOut() {
					if task.isTaskExecuted(m) {
						delete(task.getTaskExecuting(m), task.FileName)
					} else {
						delete(task.getTaskExecuting(m), task.FileName)
						*task.getTaskUnExecutes(m) = append(*task.getTaskUnExecutes(m), task)
					}
				}
			}
			for _, task := range m.ReduceExecuting {
				if task.isTimeOut() {
					if task.isTaskExecuted(m) {
						delete(task.getTaskExecuting(m), task.FileName)
					} else {
						delete(task.getTaskExecuting(m), task.FileName)
						*task.getTaskUnExecutes(m) = append(*task.getTaskUnExecutes(m), task)
					}
				}
			}
			time.Sleep(1 * time.Second)
		}
	}()
	go http.Serve(l, nil)
}

//
// main/mrmaster.go calls Done() periodically to find out
// if the entire job has finished.
//
func (m *Master) Done() bool {

	// Your code here.
	//如果reduce都执行完了
	reduceFinished := m.ReduceUnExecute != nil && len(m.ReduceUnExecute) == 0 && len(m.ReduceExecuting) == 0
	if reduceFinished {
		return true
	}

	return false
}

//
// create a Master.
// main/mrmaster.go calls this function.
// nReduce is the number of reduce tasks to use.  把中间值哈希成10份
//
func MakeMaster(files []string, nReduce int) *Master {

	m := Master{}

	//初始化未执行MapTask数组
	m.MapUnExecute = []*Task{}
	for _, file := range files {
		m.MapUnExecute = append(m.MapUnExecute, &Task{
			Type:         TYPE_MAP,
			FileName:     file,
			Status:       WAIT_FOR_EXECUTE,
			RetrieveTime: time.Now(),
			NReduce:      nReduce,
		})
	}

	//初始化 map
	m.MapExecuted = make(map[string]*Task)
	m.MapExecuting = make(map[string]*Task)
	m.NReduce = nReduce

	m.server()
	return &m
}
