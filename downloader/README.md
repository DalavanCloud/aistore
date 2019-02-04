## Why Downloader?
It is a well-known fact that some of the most popular AI datasets have Internet addresses.

> See, for instance, [Revisiting Unreasonable Effectiveness of Data in Deep Learning Era](https://arxiv.org/abs/1707.02968) - the paper lists a good number of very large and very popular datasets.

Given that fact, it is only natural to ask the follow-up question: how to work with those datasets? And what happens if the dataset in question is *larger* than a single host? Meaning, what happens if it is large enough to warrant (and require) a distributed storage system?

Meet **Internet downloader** - an integrated part of the AIStore. AIStore cluster can be quickly deployed locally to the compute clients, and the **downloader** can be then used to quickly populate a specified (distributed) AIS bucket with the objects from a given Internet location.

## Download Request

AIStore supports 3 types of download requests:

* *Single* - download a single object
* *Multi* - download multiple objects
* *List* - download multiple objects based on a given naming pattern

> - Prior to downloading, make sure that AIS (destination) bucket already exists. See [AIS API](../docs/http_api.md) for details on how to create, destroy, and list storage buckets. For Python-based clients, a better starting point could be [here](../README.md#python-client).

Rest of this document is structured around these 3 supported types of downloads:

## Table of Contents
- [Single-object download](single-object-download)
- [Multi-object download](multi-object-download)
- [List download](list-download)
- [Cancellation](cancellation)
- [Status of the download](status-of-the-download)

## Single-object download

The request (described below) downloads a *single* object and is the most basic of the three.

### Request Body Parameters

Name | Type | Description | Optional?
------------ | ------------- | ------------- | -------------
**link** | **string** | URL of where the object will be downloaded from. |
**bucket** | **string** | bucket where the download will be saved to |
**objname** | **string** | name of the object the download will be saved as. If no objname is provided, then the objname will be the last element in the URL's path. | Yes
**headers** | **JSON object** | JSON object where the key is a header name and the value is the corresponding header value (string). These values are used as the header values for when AIS actually makes the GET request to download the object from the link. | Yes

### Sample Request

| Operation | HTTP action  | Example  | Notes |
|--|--|--|--|
| Single Object Download | POST /v1/download/single | `curl -L -i -v -X POST -H 'Content-Type: application/json' -d '{"bucket": "ubuntu", "objname": "ubuntu.iso", "headers":  {  "Authorization": "Bearer AbCdEf123456" }, "link": "http://releases.ubuntu.com/18.04.1/ubuntu-18.04.1-desktop-amd64.iso"}' http://localhost:8080/v1/download/single`| Header authorization is not required to make this download request. It is just provided as an example. |

## Multi-object download

A *multi* object download requires either a map (denoted as **object_map** below) or a list (**object_list**).

### Request Parameters

Name | Type | Description | Optional?
------------ | ------------- | ------------- | -------------
**bucket** | **string** | Bucket where the downloaded objects will be saved to. |
**headers** | **object** | JSON object where the key is a header name and the value is the corresponding header value(string). These values are used as the header values for when AIS actually makes the GET request to download the object. | Yes
**object_map** | **JSON object** | JSON object where the key (string) is the objname the object will be saved as and value (string) is a URL pointing to some file. | Yes
**object_list** | **JSON array** | JSON array where each item is a URL (string) pointing to some file. The objname for each file will be the last element in the URL's path. | Yes

### Sample Request

| Operation | HTTP action | Example |
|--|--|--|
| Multi Download Using Object Map | POST /v1/download/multi | `curl -L -i -v -X POST -H 'Content-Type: application/json' -d '{"bucket": "yann-lecun", "object_map": {"t10k-images-idx3-ubyte.gz": "http://yann.lecun.com/exdb/mnist/train-labels-idx1-ubyte.gz", "t10k-labels-idx1-ubyte.gz": "http://yann.lecun.com/exdb/mnist/t10k-labels-idx1-ubyte.gz", "train-images-idx3-ubyte.gz": "http://yann.lecun.com/exdb/mnist/train-images-idx3-ubyte.gz"}}' http://localhost:8080/v1/download/multi` |
| Multi Download Using Object List |  POST /v1/download/multi  | `curl -L -i -v -X POST -H 'Content-Type: application/json' -d '{"bucket": "yann-lecun", "object_list": ["http://yann.lecun.com/exdb/mnist/train-labels-idx1-ubyte.gz", "http://yann.lecun.com/exdb/mnist/t10k-labels-idx1-ubyte.gz", "http://yann.lecun.com/exdb/mnist/train-images-idx3-ubyte.gz"]}' http://localhost:8080/v1/download/multi` |

## List download

A *list* download retrieves (in one shot) multiple objects while expecting (and relying upon) a certain naming convention which happens to be often used.

Namely, the *list* download expects the object name to consist of prefix + index + suffix, as described below:

### List Format

Consider a website named `randomwebsite.com/somedirectory/` that contains the following files:
- object1log.txt
- object2log.txt
- object3log.txt
- ...
- object1000log.txt

To populate AIStore with objects in the range from `object200log.txt` to `object300log.txt` (101 objects total), use the *list* download.

### Request Parameters

Name | Type | Description | Optional?
------------ | ------------- | ------------- | -------------
**bucket** | **string** | Bucket where the downloaded objects will be saved to. |
**headers** | **JSON object** | JSON object where the key is a header name and the value is the corresponding header value (string). These values are used as the header values for when AIS actually makes the GET request to download each object. | Yes
**base** | **string** | The base URL of the object that will be used to formulate the download url |
**prefix** | **string** | Is the first thing appended to the base string to formulate the download url | Yes
**suffix** | **string** | the suffix follows the object index to formulate the download url of the object being downloaded. | Yes
**start** | **int** | The index of the first object in the object space to be downloaded. Defualt is 0 if not provided. | Yes
**end** | **int** | The upper bound of the range of objects to be downloaded in the object space. Defualt is 0 if not provided. | Yes
**step** | **int** | Used to download every nth object (where n = step) in the object space starting from start and ending at end. Default is 1 if not provided. | Yes
**digit_count** | **int** | Used to ensure that each key coforms to n digits (where n = digit_count). Basically prepends as many 0s as needed. i.e. if n == 4, then the key 45 will be 0045 and if n == 5, then key 45 wil be 00045. Not providing this field will mean no 0s are prepended to any index in the key space. | Yes

### Sample Request

| Operation | HTTP action | Example |
|--|--|--|
| Download a List of Objects | POST /v1/download/list | `curl -L -i -v -X POST -H 'Content-Type: application/json' -d '{"download_action": "download_list", "bucket": "test321",  "base": "randomwebsite.com/somedirectory/",  "prefix": "object",  "suffix": "log.txt",  "start": 200,  "end": 300, "step": 1, "digit_count": 0 }' http://localhost:8080/v1/download/list`|

## Cancellation

Any download request can be cancelled at any time by making a DELETE request to the **downloader**.

### Request Parameters

Name | Type | Description | Optional?
------------ | ------------- | ------------- | -------------
**link** | **string** | URL of the object that was added to the download queue |
**bucket** | **string** | bucket where the download was supposed to be saved to |
**objname** | **string** | name of the object the download was supposed to be saved as |

### Sample Request

| Operation | HTTP action  | Example |
|--|--|--|
| Cancel Download | DELETE v1/download | `curl -L -i -v -X DELETE -H 'Content-Type: application/json' -d '{"bucket": "ubuntu", "objname": "ubuntu.iso", "link": "http://releases.ubuntu.com/18.04.1/ubuntu-18.04.1-desktop-amd64.iso"}' http://localhost:8080/v1/download`|

### Status of the download

Status of any download request can be queried at any time.

### Request Parameters

Name | Type | Description | Optional?
------------ | ------------- | ------------- | -------------
**link** | **string** | URL of the object that was added to the download queue |
**bucket** | **string** | bucket where the download was supposed to be saved to |
**objname** | **string** | name of the object the download was supposed to be saved as |

### Sample Request

| Operation | HTTP action | Example |
|--|--|--|
| Get Download Status | GET /v1/download/ | `curl -L -i -v -X GET -H 'Content-Type: application/json' -d '{"bucket": "ubuntu", "objname": "ubuntu.iso", "link": "http://releases.ubuntu.com/18.04.1/ubuntu-18.04.1-desktop-amd64.iso"}' http://localhost:8080/v1/download`|
