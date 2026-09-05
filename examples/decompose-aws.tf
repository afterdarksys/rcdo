resource "aws_vpc" "demo" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_subnet" "web" {
  vpc_id            = aws_vpc.demo.id
  cidr_block        = "10.42.1.0/24"
  availability_zone = "us-east-1a"
}

resource "aws_instance" "web" {
  ami           = "ami-REPLACE_ME"
  instance_type = "t3.micro"
  subnet_id     = aws_subnet.web.id
}
