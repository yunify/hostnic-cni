package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/syndtr/goleveldb/leveldb"
)

func get(db *leveldb.DB, key string) {
	if key != "" {
		if data, err := db.Get([]byte(key), nil); err != nil {
			fmt.Printf("Get %s from DB failed: %v\n", key, err)
		} else {
			fmt.Printf("Get %s from DB:\n", key)
			fmt.Printf("\t%s: %s\n", key, string(data))
		}
		return
	}

	fmt.Printf("Get all from DB:\n")
	iter := db.NewIterator(nil, nil)
	for iter.Next() {
		k := iter.Key()
		v := iter.Value()
		fmt.Printf("\t%s: %s\n", string(k), string(v))
	}

	iter.Release()
	if err := iter.Error(); err != nil {
		fmt.Printf("iter err: %v\n", err)
	}
}

func set(db *leveldb.DB, key, value string) {
	if key == "" {
		fmt.Printf("Plesse set iter's key\n")
		return
	}

	if err := db.Put([]byte(key), []byte(value), nil); err != nil {
		fmt.Printf("Set key(%s) value(%s) failed: %v\n", key, value, err)
	} else {
		fmt.Printf("Set key(%s) value(%s) OK\n", key, value)
	}
}

func del(db *leveldb.DB, key string) {
	if key == "" {
		fmt.Printf("Plesse set iter's key\n")
		return
	}

	if err := db.Delete([]byte(key), nil); err != nil {
		fmt.Printf("Del key(%s) failed: %v\n", key, err)
	} else {
		fmt.Printf("Del key(%s) OK\n", key)
	}
}

var path, op, key, value string

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), "Usage of %s:\n", os.Args[0])
	flag.PrintDefaults()
	fmt.Println("\nExamples:")
	fmt.Println("\t./client")
	fmt.Println("\t./client -op get -key t1")
	fmt.Println("\t./client -op set -key t2 -value 789")
	fmt.Println("\t./client -op del -key t2")
}

func main() {
	flag.StringVar(&path, "dbpath", "/var/lib/hostnic", "set leveldb path")
	flag.StringVar(&op, "op", "get", "operator to db")
	flag.StringVar(&key, "key", "", "iter's key")
	flag.StringVar(&value, "value", "", "iter's value")
	flag.Usage = usage
	flag.Parse()

	db, err := leveldb.OpenFile(path, nil)
	if err != nil {
		fmt.Printf("cannot open leveldb file %s : %v\n", path, err)
		return
	}
	defer db.Close()

	switch op {
	case "get":
		get(db, key)
	case "set":
		set(db, key, value)
	case "del":
		del(db, key)
	}
}
