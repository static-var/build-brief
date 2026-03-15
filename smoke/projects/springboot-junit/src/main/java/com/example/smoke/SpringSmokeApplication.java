package com.example.smoke;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

@SpringBootApplication
public class SpringSmokeApplication {
    public static void main(String[] args) {
        SpringApplication.run(SpringSmokeApplication.class, args);
    }

    @Bean
    GreetingService greetingService() {
        return new GreetingService();
    }
}
