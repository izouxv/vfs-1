package s3

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/c2fo/vfs/v6"
	"github.com/c2fo/vfs/v6/mocks"
	"github.com/c2fo/vfs/v6/options/delete"
	"github.com/c2fo/vfs/v6/utils"
)

type fileTestSuite struct {
	suite.Suite
}

var (
	s3apiMock      *mocks.S3API
	fs             FileSystem
	testFile       vfs.File
	defaultOptions Options
	testFileName   string
	bucket         string
)

func (ts *fileTestSuite) SetupTest() {
	var err error
	s3apiMock = &mocks.S3API{}
	defaultOptions = Options{AccessKeyID: "abc"}
	fs = FileSystem{client: s3apiMock, options: defaultOptions}
	testFileName = "/some/path/to/file.txt"
	bucket = "bucket"
	testFile, err = fs.NewFile(bucket, testFileName)

	if err != nil {
		ts.Fail("Shouldn't return error creating test s3.File instance.")
	}
}

func (ts *fileTestSuite) TearDownTest() {
}

func (ts *fileTestSuite) TestRead() {
	contents := "hello world!"

	file, err := fs.NewFile("bucket", "/some/path/file.txt")
	if err != nil {
		ts.Fail("Shouldn't fail creating new file")
	}

	var localFile = bytes.NewBuffer([]byte{})
	s3apiMock.
		On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{ContentLength: aws.Int64(12)}, nil).
		Once()
	s3apiMock.
		On("GetObject", mock.AnythingOfType("*s3.GetObjectInput")).
		Return(&s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(contents))}, nil).
		Once()
	_, copyErr := io.Copy(localFile, file)
	ts.NoError(copyErr, "no error expected")
	closeErr := file.Close()
	ts.NoError(closeErr, "no error expected")
	ts.Equal(contents, localFile.String(), "Copying an s3 file to a buffer should fill buffer with file's contents")

	// test read with error
	s3apiMock.
		On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{ContentLength: aws.Int64(12)}, nil).
		Once()
	s3apiMock.
		On("GetObject", mock.AnythingOfType("*s3.GetObjectInput")).
		Return(nil, errors.New("some error")).
		Once()
	_, copyErr = io.Copy(localFile, file)
	ts.Error(copyErr, "error expected")
	ts.EqualError(copyErr, "some error", "error expected")
	closeErr = file.Close()
	ts.NoError(closeErr, "no error expected")

}

// TODO: Write on Close() (actual s3 calls wait until file is closed to be made.)
func (ts *fileTestSuite) TestWrite() {
	file, err := fs.NewFile("bucket", "/tmp/hello.txt")
	ts.NoError(err, "Shouldn't fail creating new file")

	contents := []byte("Hello world!")
	count, err := file.Write(contents)

	ts.Equal(len(contents), count, "Returned count of bytes written should match number of bytes passed to Write.")
	ts.Nil(err, "Error should be nil when calling Write")
}

func (ts *fileTestSuite) TestSeek() {
	contents := "hello world!"
	file, err := fs.NewFile("bucket", "/tmp/hello.txt")
	ts.NoError(err, "Shouldn't fail creating new file")

	// setup mock for Size(getHeadObject)
	headOutput := &s3.HeadObjectOutput{ContentLength: aws.Int64(12)}

	testCases := []struct {
		seekOffset  int64
		seekWhence  int
		expectedPos int64
		expectedErr bool
		readContent string
	}{
		{6, 0, 6, false, "world!"},
		{0, 0, 0, false, contents},
		{0, 2, 12, false, ""},
		{-1, 0, 0, true, ""}, // Seek before start
		{0, 3, 0, true, ""},  // bad whence
	}

	for _, tc := range testCases {
		s3apiMock.
			On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
			Return(headOutput, nil).
			Once()
		localFile := bytes.NewBuffer([]byte{})
		pos, err := file.Seek(tc.seekOffset, tc.seekWhence)

		if tc.expectedErr {
			ts.Error(err, "Expected error for seek offset %d and whence %d", tc.seekOffset, tc.seekWhence)
		} else {
			ts.NoError(err, "No error expected for seek offset %d and whence %d", tc.seekOffset, tc.seekWhence)
			ts.Equal(tc.expectedPos, pos, "Expected position does not match for seek offset %d and whence %d", tc.seekOffset, tc.seekWhence)

			// Mock the GetObject call
			s3apiMock.
				On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
				Return(headOutput, nil).
				Once()
			s3apiMock.On("GetObject", mock.AnythingOfType("*s3.GetObjectInput")).
				Return(&s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(tc.readContent))}, nil).
				Once()

			_, err = io.Copy(localFile, file)
			ts.NoError(err, "No error expected during io.Copy")
			ts.Equal(tc.readContent, localFile.String(), "Content does not match after seek and read")
		}
	}

	// test fails with Size error
	s3apiMock.
		On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(nil, awserr.New("NotFound", "file does not exist", errors.New("file not found"))).
		Once()
	_, err = file.Seek(0, 0)
	ts.Require().Error(err, "error expected")
	ts.Require().ErrorIs(err, vfs.ErrNotExist, "error expected")

	err = file.Close()
	ts.NoError(err, "Closing file should not produce an error")
}

func (ts *fileTestSuite) TestGetLocation() {
	file, err := fs.NewFile("bucket", "/path/hello.txt")
	ts.NoError(err, "Shouldn't fail creating new file.")

	location := file.Location()
	ts.Equal("s3", location.FileSystem().Scheme(), "Should initialize location with FS underlying file.")
	ts.Equal("/path/", location.Path(), "Should initialize path with the location of the file.")
	ts.Equal("bucket", location.Volume(), "Should initialize bucket with the bucket containing the file.")
}

func (ts *fileTestSuite) TestExists() {
	file, err := fs.NewFile("bucket", "/path/hello.txt")
	if err != nil {
		ts.Fail("Shouldn't fail creating new file.")
	}

	s3apiMock.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{}, nil)

	exists, err := file.Exists()
	ts.True(exists, "Should return true for exists based on this setup")
	ts.Nil(err, "Shouldn't return an error when exists is true")
}

func (ts *fileTestSuite) TestNotExists() {
	file, err := fs.NewFile("bucket", "/path/hello.txt")
	if err != nil {
		ts.Fail("Shouldn't fail creating new file.")
	}

	s3apiMock.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{}, awserr.New(s3.ErrCodeNoSuchKey, "key doesn't exist", nil))

	exists, err := file.Exists()
	ts.False(exists, "Should return false for exists based on setup")
	ts.Nil(err, "Error from key not existing should be hidden since it just confirms it doesn't")
}

func (ts *fileTestSuite) TestCopyToFile() {
	targetFile := &File{
		fileSystem: &FileSystem{
			client:  s3apiMock,
			options: defaultOptions,
		},
		bucket: "TestBucket",
		key:    "testKey.txt",
	}

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(&s3.CopyObjectOutput{}, nil)

	err := testFile.CopyToFile(targetFile)
	ts.Nil(err, "Error shouldn't be returned from successful call to CopyToFile")
	s3apiMock.AssertExpectations(ts.T())

	// Test With Non Minimum Buffer Size in TouchCopyBuffered
	originalBufferSize := defaultOptions.FileBufferSize
	defaultOptions.FileBufferSize = 2 * utils.TouchCopyMinBufferSize
	targetFile = &File{
		fileSystem: &FileSystem{
			client:  s3apiMock,
			options: defaultOptions,
		},
		bucket: "TestBucket",
		key:    "testKey.txt",
	}
	defaultOptions.FileBufferSize = originalBufferSize

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(&s3.CopyObjectOutput{}, nil)

	err = testFile.CopyToFile(targetFile)
	ts.Nil(err, "Error shouldn't be returned from successful call to CopyToFile")
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestEmptyCopyToFile() {
	targetFile := &mocks.File{}
	targetFile.On("Write", mock.Anything).Return(0, nil)
	targetFile.On("Close").Return(nil)
	s3apiMock.
		On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{ContentLength: aws.Int64(0)}, nil).
		Once()
	s3apiMock.
		On("GetObject", mock.AnythingOfType("*s3.GetObjectInput")).
		Return(&s3.GetObjectOutput{Body: io.NopCloser(strings.NewReader(""))}, nil).
		Once()
	err := testFile.CopyToFile(targetFile)
	ts.Nil(err, "Error shouldn't be returned from successful call to CopyToFile")

	// Assert that file was still written to and closed when the reader size is 0 bytes.
	targetFile.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestMoveToFile() {
	targetFile := &File{
		fileSystem: &FileSystem{
			client:  s3apiMock,
			options: defaultOptions,
		},
		bucket: "TestBucket",
		key:    "testKey.txt",
	}

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(&s3.CopyObjectOutput{}, nil)
	s3apiMock.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(&s3.DeleteObjectOutput{}, nil)

	err := testFile.MoveToFile(targetFile)
	ts.Nil(err, "Error shouldn't be returned from successful call to MoveToFile")
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestGetCopyObject() {
	type getCopyObjectTest struct {
		key, expectedCopySource string
	}
	tests := []getCopyObjectTest{
		{
			key:                "/path/to/nospace.txt",
			expectedCopySource: "%2Fpath%2Fto%2Fnospace.txt",
		},
		{
			key:                "/path/to/has space.txt",
			expectedCopySource: "%2Fpath%2Fto%2Fhas%20space.txt",
		},
		{
			key:                "/path/to/encoded%20space.txt",
			expectedCopySource: "%2Fpath%2Fto%2Fencoded%2520space.txt",
		},
		{
			key:                "/path/to/has space/file.txt",
			expectedCopySource: "%2Fpath%2Fto%2Fhas%20space%2Ffile.txt",
		},
		{
			key:                "/path/to/encoded%20space/file.txt",
			expectedCopySource: "%2Fpath%2Fto%2Fencoded%2520space%2Ffile.txt",
		},
	}

	// ensure spaces are properly encoded (or not)
	for _, t := range tests {
		sourceFile := &File{
			fileSystem: &FileSystem{
				client: s3apiMock,
				options: Options{
					AccessKeyID:                 "abc",
					DisableServerSideEncryption: true,
				},
			},
			bucket: "TestBucket",
			key:    t.key,
		}

		targetFile := &File{
			fileSystem: &FileSystem{
				client: s3apiMock,
				options: Options{
					AccessKeyID: "abc",
				},
			},
			bucket: "TestBucket",
			key:    "source.txt",
		}

		// copy from t.key to /source.txt
		actual, err := sourceFile.getCopyObjectInput(targetFile)
		ts.Nil(err, "Error shouldn't be returned from successful call to CopyToFile")
		ts.Equal("TestBucket"+t.expectedCopySource, *actual.CopySource)
		ts.Nil(actual.ServerSideEncryption, "sse is disabled")
	}

	// test that different options returns nil
	// nil means we can't do s3-to-s3 copy so use TouchCopy
	sourceFile := &File{
		fileSystem: &FileSystem{
			client:  s3apiMock,
			options: defaultOptions,
		},
		bucket: "TestBucket",
		key:    "/path/to/file.txt",
	}

	targetFile := &File{
		fileSystem: &FileSystem{
			client: s3apiMock,
			options: Options{AccessKeyID: "xyz",
				ACL: "SomeCannedACL",
			},
		},
		bucket: "TestBucket",
		key:    "/path/to/otherFile.txt",
	}
	actual, err := sourceFile.getCopyObjectInput(targetFile)
	ts.Nil(err, "Error shouldn't be returned from successful call to CopyToFile")
	ts.Nil(actual, "copyOjbectInput should be nil (can't do s3-to-s3 copyObject)")

	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestMoveToFile_CopyError() {
	targetFile := &File{
		fileSystem: &FileSystem{
			client:  s3apiMock,
			options: defaultOptions,
		},
		bucket: "TestBucket",
		key:    "testKey.txt",
	}

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(nil, errors.New("some copy error"))

	err := testFile.MoveToFile(targetFile)
	ts.NotNil(err, "Error shouldn't be returned from successful call to CopyToFile")
	s3apiMock.AssertNotCalled(ts.T(), "DeleteObject", mock.Anything)
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestCopyToLocation() {
	s3Mock1 := &mocks.S3API{}
	fooReader := io.NopCloser(strings.NewReader("blah"))
	s3Mock1.On("GetObject", mock.AnythingOfType("*s3.GetObjectInput")).Return(&s3.GetObjectOutput{Body: fooReader}, nil)
	s3Mock1.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(nil, nil)
	s3Mock1.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{}, nil)
	f := &File{
		fileSystem: &FileSystem{
			client:  s3Mock1,
			options: defaultOptions,
		},
		bucket: "bucket",
		key:    "/hello.txt",
	}

	defer func() {
		closeErr := f.Close()
		assert.NoError(ts.T(), closeErr, "no error expected")
	}()

	l := &Location{
		fileSystem: &FileSystem{
			client:  &mocks.S3API{},
			options: defaultOptions,
		},
		bucket: "bucket",
		prefix: "/subdir/",
	}

	// no error "copying" objects
	_, err := f.CopyToLocation(l)
	ts.NoError(err, "Shouldn't return error for this call to CopyToLocation")

}

func (ts *fileTestSuite) TestTouch() {
	// Copy portion tested through CopyToLocation, just need to test whether or not Delete happens
	// in addition to CopyToLocation

	s3Mock1 := &mocks.S3API{}
	s3Mock1.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{}, nil)
	s3Mock1.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(nil, nil)
	s3Mock1.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(&s3.DeleteObjectOutput{}, nil)

	file := &File{
		fileSystem: &FileSystem{
			client:  s3Mock1,
			options: defaultOptions,
		},
		bucket: "newBucket",
		key:    "/new/file/path/hello.txt",
	}

	terr := file.Touch()
	ts.NoError(terr, "Shouldn't return error creating test s3.File instance.")

	s3Mock1.AssertExpectations(ts.T())

	// test non-existent length
	s3Mock2 := &mocks.S3API{}
	s3Mock2.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{}, awserr.New(s3.ErrCodeNoSuchKey, "", nil)).Once()
	s3Mock2.On("PutObjectRequest", mock.AnythingOfType("*s3.PutObjectInput")).
		Return(&request.Request{HTTPRequest: &http.Request{Header: make(map[string][]string), URL: &url.URL{}}}, &s3.PutObjectOutput{})
	s3Mock2.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{}, nil)
	file2 := &File{
		fileSystem: &FileSystem{
			client:  s3Mock2,
			options: defaultOptions,
		},
		bucket: "newBucket",
		key:    "/new/file/path/hello.txt",
	}
	terr2 := file2.Touch()
	ts.NoError(terr2, "Shouldn't return error creating test s3.File instance.")

	s3Mock2.AssertExpectations(ts.T())

}

func (ts *fileTestSuite) TestMoveToLocation() {
	// Copy portion tested through CopyToLocation, just need to test whether or not Delete happens
	// in addition to CopyToLocation
	s3Mock1 := &mocks.S3API{}
	s3Mock1.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(nil, nil)
	s3Mock1.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{}, nil)
	f := &File{
		fileSystem: &FileSystem{
			client:  s3Mock1,
			options: defaultOptions,
		},
		bucket: "newBucket",
		key:    "/new/file/path/hello.txt",
	}
	location := new(mocks.Location)
	location.On("NewFile", mock.Anything).Return(f, nil)

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(&s3.CopyObjectOutput{}, nil)
	s3apiMock.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(&s3.DeleteObjectOutput{}, nil)

	file, err := fs.NewFile("bucket", "/hello.txt")
	if err != nil {
		ts.Fail("Shouldn't return error creating test s3.File instance.")
	}

	defer func() {
		closeErr := file.Close()
		ts.NoError(closeErr, "no error expected")
	}()

	_, err = file.MoveToLocation(location)
	ts.NoError(err, "no error expected")

	// test non-scheme MoveToLocation
	mockLocation := new(mocks.Location)
	mockLocation.On("NewFile", mock.Anything).
		Return(&File{fileSystem: &FileSystem{client: s3Mock1}, bucket: "bucket", key: "/new/hello.txt"}, nil)

	s3apiMock2 := &mocks.S3API{}
	s3apiMock2.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(&s3.CopyObjectOutput{}, nil)

	fs = FileSystem{client: s3apiMock2}
	file2, err := fs.NewFile("bucket", "/hello.txt")
	if err != nil {
		ts.Fail("Shouldn't return error creating test s3.File instance.")
	}

	_, err = file2.CopyToLocation(mockLocation)
	ts.NoError(err, "MoveToLocation error not expected")

	s3apiMock.AssertExpectations(ts.T())
	location.AssertExpectations(ts.T())
	mockLocation.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestMoveToLocationFail() {

	// If CopyToLocation fails we need to ensure DeleteObject isn't called.
	otherFs := new(mocks.FileSystem)
	location := new(mocks.Location)
	location.On("NewFile", mock.Anything).Return(&File{fileSystem: &fs, bucket: "bucket", key: "/new/hello.txt"}, nil)

	s3apiMock.On("CopyObject", mock.AnythingOfType("*s3.CopyObjectInput")).Return(nil, errors.New("didn't copy, oh noes"))

	file, err := fs.NewFile("bucket", "/hello.txt")
	if err != nil {
		ts.Fail("Shouldn't return error creating test s3.File instance.")
	}

	_, merr := file.MoveToLocation(location)
	ts.Error(merr, "MoveToLocation error not expected")

	closeErr := file.Close()
	ts.NoError(closeErr, "no close error expected")

	s3apiMock.AssertExpectations(ts.T())
	s3apiMock.AssertNotCalled(ts.T(), "DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput"))
	otherFs.AssertExpectations(ts.T())
	location.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestDelete() {
	s3apiMock.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(&s3.DeleteObjectOutput{}, nil)
	err := testFile.Delete()
	ts.Nil(err, "Successful delete should not return an error.")
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestDeleteError() {
	s3apiMock.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(nil, errors.New("something went wrong"))
	err := testFile.Delete()
	ts.NotNil(err, "Delete should return an error if s3 api had error.")
	ts.Equal(err.Error(), "something went wrong")
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestDeleteWithDeleteAllVersionsOption() {
	var versions []*s3.ObjectVersion
	verIds := [...]string{"ver1", "ver2"}
	for i := range verIds {
		versions = append(versions, &s3.ObjectVersion{VersionId: &verIds[i]})
	}
	versOutput := s3.ListObjectVersionsOutput{
		Versions: versions,
	}
	s3apiMock.On("ListObjectVersions", mock.AnythingOfType("*s3.ListObjectVersionsInput")).Return(&versOutput, nil)
	s3apiMock.On("DeleteObject", mock.AnythingOfType("*s3.DeleteObjectInput")).Return(&s3.DeleteObjectOutput{}, nil)

	err := testFile.Delete(delete.WithDeleteAllVersions())
	ts.Nil(err, "Successful delete should not return an error.")
	s3apiMock.AssertExpectations(ts.T())
	s3apiMock.AssertNumberOfCalls(ts.T(), "DeleteObject", 3)
}

func (ts *fileTestSuite) TestDeleteWithDeleteAllVersionsOptionError() {
	var versions []*s3.ObjectVersion
	verIds := [...]string{"ver1", "ver2"}
	for i := range verIds {
		versions = append(versions, &s3.ObjectVersion{VersionId: &verIds[i]})
	}
	versOutput := s3.ListObjectVersionsOutput{
		Versions: versions,
	}
	s3apiMock.On("ListObjectVersions", mock.AnythingOfType("*s3.ListObjectVersionsInput")).Return(&versOutput, nil)
	s3apiMock.On("DeleteObject", &s3.DeleteObjectInput{Key: &testFileName, Bucket: &bucket}).Return(&s3.DeleteObjectOutput{}, nil)
	s3apiMock.On("DeleteObject", &s3.DeleteObjectInput{Key: &testFileName, Bucket: &bucket, VersionId: &verIds[0]}).
		Return(nil, errors.New("something went wrong"))

	err := testFile.Delete(delete.WithDeleteAllVersions())
	ts.NotNil(err, "Delete should return an error if s3 api had error.")
	s3apiMock.AssertExpectations(ts.T())
	s3apiMock.AssertNumberOfCalls(ts.T(), "DeleteObject", 2)
}

func (ts *fileTestSuite) TestLastModified() {
	now := time.Now()
	s3apiMock.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{
		LastModified: &now,
	}, nil)
	modTime, err := testFile.LastModified()
	ts.Nil(err, "Error should be nil when correctly returning time of object.")
	ts.Equal(&now, modTime, "Returned time matches expected LastModified time.")
}

func (ts *fileTestSuite) TestLastModifiedFail() {
	// setup error on HEAD
	s3apiMock.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(nil,
		errors.New("boom"))
	m, e := testFile.LastModified()
	ts.Error(e, "got error as exepcted")
	ts.Nil(m, "nil ModTime returned")
}

func (ts *fileTestSuite) TestName() {
	ts.Equal("file.txt", testFile.Name(), "Name should return just the name of the file.")
}

func (ts *fileTestSuite) TestSize() {
	contentLength := int64(100)
	s3apiMock.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).Return(&s3.HeadObjectOutput{
		ContentLength: &contentLength,
	}, nil)

	size, err := testFile.Size()
	ts.Nil(err, "Error should be nil when requesting size for file that exists.")
	ts.Equal(uint64(100), size, "Size should return the ContentLength value from s3 HEAD request.")
	s3apiMock.AssertExpectations(ts.T())
}

func (ts *fileTestSuite) TestPath() {
	ts.Equal("/some/path/to/file.txt", testFile.Path(), "Should return file.key (with leading slash)")
}

func (ts *fileTestSuite) TestURI() {
	s3apiMock = &mocks.S3API{}
	fs = FileSystem{client: s3apiMock}
	file, _ := fs.NewFile("mybucket", "/some/file/test.txt")
	expected := "s3://mybucket/some/file/test.txt"
	ts.Equal(expected, file.URI(), "%s does not match %s", file.URI(), expected)
}

func (ts *fileTestSuite) TestStringer() {
	fs = FileSystem{client: &mocks.S3API{}}
	file, _ := fs.NewFile("mybucket", "/some/file/test.txt")
	ts.Equal("s3://mybucket/some/file/test.txt", file.String())
}

func (ts *fileTestSuite) TestUploadInput() {
	fs = FileSystem{client: &mocks.S3API{}}
	file, _ := fs.NewFile("mybucket", "/some/file/test.txt")
	ts.Equal("AES256", *uploadInput(file.(*File)).ServerSideEncryption, "sse was set")
	ts.Equal("/some/file/test.txt", *uploadInput(file.(*File)).Key, "key was set")
	ts.Equal("mybucket", *uploadInput(file.(*File)).Bucket, "bucket was set")
}

func (ts *fileTestSuite) TestUploadInputDisableSSE() {
	fs := NewFileSystem().
		WithOptions(Options{DisableServerSideEncryption: true})
	file, _ := fs.NewFile("mybucket", "/some/file/test.txt")
	input := uploadInput(file.(*File))
	ts.Nil(input.ServerSideEncryption, "sse was disabled")
	ts.Equal("/some/file/test.txt", *input.Key, "key was set")
	ts.Equal("mybucket", *input.Bucket, "bucket was set")
}

func (ts *fileTestSuite) TestNewFile() {
	fs := &FileSystem{}
	// fs is nil
	_, err := fs.NewFile("", "")
	ts.Errorf(err, "non-nil s3.FileSystem pointer is required")

	// bucket is ""
	_, err = fs.NewFile("", "asdf")
	ts.Errorf(err, "non-empty strings for bucket and key are required")
	// key is ""
	_, err = fs.NewFile("asdf", "")
	ts.Errorf(err, "non-empty strings for bucket and key are required")

	//
	bucket := "mybucket"
	key := "/path/to/key"
	file, err := fs.NewFile(bucket, key)
	ts.NoError(err, "newFile should succeed")
	ts.IsType(&File{}, file, "newFile returned a File struct")
	ts.Equal(bucket, file.Location().Volume())
	ts.Equal(key, file.Path())
}

func (ts *fileTestSuite) TestCloseWithoutWrite() {
	fs := &FileSystem{}
	file, err := fs.NewFile("mybucket", "/some/file/test.txt")
	ts.NoError(err)
	ts.NoError(file.Close())
	ts.NoError(err, "file closed without error")
}

func (ts *fileTestSuite) TestCloseWithWrite() {
	s3Mock2 := &mocks.S3API{}
	s3Mock2.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{}, awserr.New(s3.ErrCodeNoSuchKey, "", nil)).Once()
	s3Mock2.On("PutObjectRequest", mock.AnythingOfType("*s3.PutObjectInput")).
		Return(&request.Request{HTTPRequest: &http.Request{Header: make(map[string][]string), URL: &url.URL{}}}, &s3.PutObjectOutput{})
	s3Mock2.On("HeadObject", mock.AnythingOfType("*s3.HeadObjectInput")).
		Return(&s3.HeadObjectOutput{}, awserr.New(s3.ErrCodeNoSuchKey, "key doesn't exist", nil))
	file := &File{
		fileSystem: &FileSystem{
			client:  s3Mock2,
			options: defaultOptions,
		},
		bucket: "newBucket",
		key:    "/new/file/path/hello.txt",
	}
	contents := []byte("Hello world!")
	_, err := file.Write(contents)
	ts.NoError(err, "Error should be nil when calling Write")
	err = file.Close()
	ts.Error(err, "file doesn't exists , retired 5 times ")

}

// TestSeekTo tests the seekTo function with various cases
func (ts *fileTestSuite) TestSeekTo() {
	testCases := []struct {
		position         int64
		offset           int64
		whence           int
		length           int64
		expectedPosition int64
		expectError      error
	}{
		// Test seeking from start
		{0, 10, io.SeekStart, 100, 10, nil},
		{0, -10, io.SeekStart, 100, 0, vfs.ErrSeekInvalidOffset}, // Negative offset from start
		{0, 110, io.SeekStart, 100, 110, nil},                    // Offset beyond length

		// Test seeking from current position
		{50, 10, io.SeekCurrent, 100, 60, nil},
		{50, -60, io.SeekCurrent, 100, 0, vfs.ErrSeekInvalidOffset}, // Moving before start
		{50, 60, io.SeekCurrent, 100, 110, nil},                     // Moving beyond length

		// Test seeking from end
		{0, -10, io.SeekEnd, 100, 90, nil},
		{0, -110, io.SeekEnd, 100, 0, vfs.ErrSeekInvalidOffset}, // Moving before start
		{0, 10, io.SeekEnd, 100, 110, nil},                      // Moving beyond length

		// Additional edge cases
		{0, 0, io.SeekStart, 100, 0, nil},       // No movement from start
		{100, 0, io.SeekCurrent, 100, 100, nil}, // No movement from current
		{0, 0, io.SeekEnd, 100, 100, nil},       // No movement from end

		// invalid whence case
		{0, 0, 3, 100, 0, vfs.ErrSeekInvalidWhence},
	}

	for _, tc := range testCases {
		result, err := seekTo(tc.length, tc.position, tc.offset, tc.whence)

		if tc.expectError != nil {
			ts.Error(err, "error expected")
			ts.ErrorIs(err, tc.expectError)
		} else {
			ts.NoError(err, "no error expected")
			ts.Equal(tc.expectedPosition, result)
		}
	}
}

func TestFile(t *testing.T) {
	suite.Run(t, new(fileTestSuite))
}
